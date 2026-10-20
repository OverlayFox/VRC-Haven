package dashboard

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	defaultPort = 8080

	RoleFlagship = "flagship"
	RoleEscort   = "escort"
)

//go:embed static
var staticFiles embed.FS

type Config struct {
	Port    int
	Address string
	Role    string

	// Username and Password protect the dashboard with basic auth, it is open if Password is empty
	Username string
	Password string
}

// ConfigFromEnv reads DASHBOARD_PORT (default 80), DASHBOARD_USER and DASHBOARD_PASSWORD.
func ConfigFromEnv(role string) (Config, error) {
	config := Config{
		Port:     defaultPort,
		Address:  "0.0.0.0",
		Role:     role,
		Username: os.Getenv("DASHBOARD_USER"),
		Password: os.Getenv("DASHBOARD_PASSWORD"),
	}
	if port := os.Getenv("DASHBOARD_PORT"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return Config{}, fmt.Errorf("invalid DASHBOARD_PORT '%s': %w", port, err)
		}
		config.Port = p
	}
	return config, config.Validate()
}

func (c Config) Validate() error {
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	if c.Role != RoleFlagship && c.Role != RoleEscort {
		return fmt.Errorf("invalid role: '%s'", c.Role)
	}
	return nil
}

// Section is one panel of the dashboard. Key selects the renderer in static/app.js, sections without one are shown as raw JSON.
type Section struct {
	Key   string
	Stats func() any
}

// NewSection wraps a typed stats getter, so methods like haven.EscortStats can be passed directly.
func NewSection[T any](key string, stats func() T) Section {
	return Section{Key: key, Stats: func() any { return stats() }}
}

type sectionStatus struct {
	Key  string `json:"key"`
	Data any    `json:"data"`
}

type status struct {
	Role     string          `json:"role"`
	UptimeMs int64           `json:"uptime_ms"`
	Sections []sectionStatus `json:"sections"`
}

type Server struct {
	logger zerolog.Logger
	config Config

	httpServer *http.Server
	handler    http.Handler
	sections   []Section
	startedAt  time.Time

	wg sync.WaitGroup
}

func New(logger zerolog.Logger, config Config, sections ...Section) (*Server, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	s := &Server{
		logger:    logger.With().Str("component", "dashboard").Logger(),
		config:    config,
		sections:  sections,
		startedAt: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.serveStatus)
	mux.Handle("GET /", http.FileServerFS(static))
	s.handler = s.secure(mux)

	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", config.Address, config.Port),
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s, nil
}

func (s *Server) Start() {
	if s.config.Password == "" {
		s.logger.Warn().Msg("Dashboard has no DASHBOARD_PASSWORD set, anyone who can reach it sees viewer IPs")
	}
	s.wg.Go(func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error().Err(err).Msg("Dashboard server failed, ports below 1024 need root or CAP_NET_BIND_SERVICE")
		}
	})
	s.logger.Info().Str("address", s.config.Address).Int("port", s.config.Port).Msg("Dashboard server started")
}

func (s *Server) Close() {
	_ = s.httpServer.Shutdown(context.Background())
	s.wg.Wait()

	s.logger.Info().Msg("Dashboard server stopped")
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) serveStatus(w http.ResponseWriter, _ *http.Request) {
	st := status{
		Role:     s.config.Role,
		UptimeMs: time.Since(s.startedAt).Milliseconds(),
		Sections: make([]sectionStatus, 0, len(s.sections)),
	}
	for _, section := range s.sections {
		st.Sections = append(st.Sections, sectionStatus{Key: section.Key, Data: section.Stats()})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(st); err != nil {
		s.logger.Debug().Err(err).Msg("Failed to write dashboard status")
	}
}

// secure adds basic auth if a password is configured and headers that keep the page from being framed or running foreign scripts.
func (s *Server) secure(next http.Handler) http.Handler {
	// Comparing hashes keeps the comparison constant time regardless of the input lengths
	wantUser := sha256.Sum256([]byte(s.config.Username))
	wantPass := sha256.Sum256([]byte(s.config.Password))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		if s.config.Password != "" {
			user, pass, ok := r.BasicAuth()
			gotUser := sha256.Sum256([]byte(user))
			gotPass := sha256.Sum256([]byte(pass))
			userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1
			passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1
			if !ok || !userOK || !passOK {
				w.Header().Set("WWW-Authenticate", `Basic realm="VRC-Haven", charset="UTF-8"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
