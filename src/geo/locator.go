package geo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

const (
	defaultTimeout = 30 * time.Second
	dbMaxAge       = 7 * 24 * time.Hour // 1 week
	downloadURL    = "https://download.maxmind.com/geoip/databases/GeoLite2-City/download?suffix=tar.gz"
)

// Locator handles IP-to-Location lookups via the local Database.
type Locator struct {
	config Config
	logger zerolog.Logger

	db    *geoip2.Reader
	dbMtx sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
}

// NewLocator creates a new Locator. It attempts to load a local DB immediately.
// If the DB is missing or old, and a token is provided, it triggers a background update.
func NewLocator(upstreamCtx context.Context, logger zerolog.Logger, config Config) (types.Locator, error) {
	ctx, cancel := context.WithCancel(upstreamCtx)
	l := &Locator{
		config: config,
		logger: logger.With().Str("component", "geo").Logger(),

		ctx:    ctx,
		cancel: cancel,
	}
	return l, l.loadDatabase()
}

// GetLocation returns the latitude and longitude for a given network address.
func (l *Locator) GetLocation(addr net.Addr) (types.Location, error) {
	ipStr, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		ipStr = addr.String()
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return types.Location{}, fmt.Errorf("invalid IP address: %s", ipStr)
	}

	l.dbMtx.RLock()
	defer l.dbMtx.RUnlock()

	record, err := l.db.City(ip)
	if err != nil {
		return types.Location{}, err
	}
	if record.Location.Latitude == 0 && record.Location.Longitude == 0 {
		return types.Location{}, errors.New("ip not found in database")
	}

	state := "Unknown"
	if len(record.Subdivisions) > 0 {
		state = record.Subdivisions[0].Names["en"]
	}

	return types.Location{
		Latitude:    record.Location.Latitude,
		Longitude:   record.Location.Longitude,
		CountryName: record.Country.Names["en"],
		StateName:   state,
		City:        record.City.Names["en"],
	}, nil
}

// Close cleans up resources.
func (l *Locator) Close() error {
	l.logger.Info().Msg("Shutting down geo locator")
	l.cancel()

	l.dbMtx.Lock()
	defer l.dbMtx.Unlock()

	if l.db != nil {
		return l.db.Close()
	}
	return nil
}

// loadDatabase finds the newest .mmdb file in the dbDir, checks its age, and loads it if valid.
func (l *Locator) loadDatabase() error {
	entries, err := os.ReadDir(l.config.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return l.updateDatabase()
		}
		return err
	}

	var newestFile string
	var newestTime time.Time

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".mmdb") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newestFile = filepath.Join(l.config.Dir, e.Name())
		}
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".mmdb") {
			continue
		}
		fullPath := filepath.Join(l.config.Dir, e.Name())
		if fullPath == newestFile {
			continue
		}
		if err := os.Remove(fullPath); err != nil {
			l.logger.Warn().Err(err).Str("file", e.Name()).Msg("Failed to remove old database file")
		} else {
			l.logger.Info().Str("file", e.Name()).Msg("Removed old database file")
		}
	}

	if newestFile == "" || time.Since(newestTime) > dbMaxAge {
		return l.updateDatabase()
	}

	return l.mountDatabase(newestFile)
}

func (l *Locator) mountDatabase(path string) error {
	db, err := geoip2.Open(path)
	if err != nil {
		return fmt.Errorf("failed to load database: %w", err)
	}

	l.dbMtx.Lock()
	defer l.dbMtx.Unlock()

	if l.db != nil {
		err = l.db.Close()
		if err != nil {
			return fmt.Errorf("failed to close old database: %w", err)
		}
	}
	l.db = db

	return nil
}

func (l *Locator) updateDatabase() error {
	l.logger.Info().Msg("Database is too old, updating")

	zipBytes, err := l.downloadDatabase()
	if err != nil {
		return fmt.Errorf("failed to download database file: %w", err)
	}

	mmdbBytes, err := extractMMDB(zipBytes)
	if err != nil {
		return fmt.Errorf("failed to extract .mmdb file: %w", err)
	}

	if err := os.MkdirAll(l.config.Dir, 0o750); err != nil {
		return fmt.Errorf("failed to make directory: %w", err)
	}

	filename := fmt.Sprintf("%s_bd9lite.mmdb", time.Now().Format("2006_01_02"))
	fullPath := filepath.Join(l.config.Dir, filename)
	if err := os.WriteFile(fullPath, mmdbBytes, 0o600); err != nil {
		return fmt.Errorf("failed to write .mmdb file: %w", err)
	}

	if err := l.mountDatabase(fullPath); err != nil {
		return fmt.Errorf("failed to load new database: %w", err)
	}

	l.logger.Info().Msg("Database updated successfully")
	return nil
}

func (l *Locator) downloadDatabase() ([]byte, error) {
	httpClient := &http.Client{Timeout: defaultTimeout}

	req, err := http.NewRequestWithContext(l.ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(l.config.AccountID, l.config.LicenseKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if response is a tar.gz file
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8B {
		errorMsg := strings.TrimSpace(string(body))
		if len(errorMsg) > 200 {
			errorMsg = errorMsg[:200] + "..."
		}
		return nil, fmt.Errorf("API returned error instead of tar.gz file: %s", errorMsg)
	}

	return body, nil
}

func extractMMDB(tarGzData []byte) ([]byte, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(tarGzData))
	if err != nil {
		return nil, err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar: %w", err)
		}
		if strings.HasSuffix(strings.ToLower(header.Name), ".mmdb") {
			return io.ReadAll(tarReader)
		}
	}
	return nil, errors.New("no .mmdb found in archive")
}
