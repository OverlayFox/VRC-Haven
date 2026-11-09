//go:build e2e

// Package e2e_test runs the haven end to end: an SRT publisher pushes a pre-encoded clip and the tests read the HLS output like a player would.
// The clip is pushed byte for byte, nothing here encodes, decodes or transcodes media.
package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	gosrt "github.com/datarhei/gosrt"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
	"github.com/OverlayFox/VRC-Haven/src/types"
)

const (
	testStreamID   = "e2e"
	testPassphrase = "e2e-passphrase-0123"

	tsPacketSize = 188
	// srtChunkPackets is how many TS packets go into one SRT packet, 7*188 = 1316 bytes is what OBS and ffmpeg send.
	srtChunkPackets = 7

	pcrClockHz = 27_000_000

	playlistReadyTimeout = 45 * time.Second
)

var errNoGeoDatabase = errors.New("no geo database in e2e tests")

// stubLocator never finds a location, so connections are served locally like a loopback client of a real flagship.
type stubLocator struct{}

func (stubLocator) GetLocation(net.Addr) (types.Location, error) {
	return types.Location{}, errNoGeoDatabase
}

// syncBuffer collects the server's logs, they are only printed when a test fails.
type syncBuffer struct {
	mtx sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return b.buf.String()
}

type testServer struct {
	srtAddr string
	hlsBase string // http://host:port/<stream>/<passphrase>/
}

func (s *testServer) playlistURL() string {
	return s.hlsBase + "index.m3u8"
}

// startServer runs a haven with its SRT ingest and HLS egress in process, without the geo database or public IP lookup the flagship binary needs.
func startServer(t *testing.T) *testServer {
	t.Helper()

	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("server logs:\n%s", logs.String())
		}
	})
	logger := zerolog.New(logs).Level(zerolog.InfoLevel).With().Timestamp().Logger()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h, err := haven.NewHaven(ctx, logger, haven.Config{IsFlagship: true}, nil, testPassphrase, testStreamID)
	if err != nil {
		t.Fatalf("create haven: %v", err)
	}
	t.Cleanup(h.Close)

	var hlsConfig hls.Config
	hlsConfig.SetDefaults()
	hlsConfig.Address = "127.0.0.1"
	hlsConfig.Port = freePort(t, "tcp")
	hlsServer, err := hls.New(ctx, logger, hlsConfig, h, stubLocator{})
	if err != nil {
		t.Fatalf("create HLS server: %v", err)
	}
	hlsServer.Start()
	t.Cleanup(hlsServer.Close)

	var srtConfig srt.Config
	srtConfig.SetDefaults()
	srtConfig.Address = "127.0.0.1"
	srtConfig.Port = freePort(t, "udp")
	srtConfig.StreamID = testStreamID
	srtConfig.Passphrase = testPassphrase
	srtServer, err := srt.New(ctx, logger, srtConfig, h, stubLocator{})
	if err != nil {
		t.Fatalf("create SRT server: %v", err)
	}
	srtServer.Start()
	t.Cleanup(srtServer.Close)

	hlsAddr := net.JoinHostPort(hlsConfig.Address, strconv.Itoa(hlsConfig.Port))
	waitForTCP(t, hlsAddr)

	return &testServer{
		srtAddr: net.JoinHostPort(srtConfig.Address, strconv.Itoa(srtConfig.Port)),
		hlsBase: fmt.Sprintf("http://%s/%s/%s/", hlsAddr, testStreamID, testPassphrase),
	}
}

// freePort returns a port the OS considers free on loopback, the servers do not report the port they bound.
func freePort(t *testing.T, network string) int {
	t.Helper()

	switch network {
	case "tcp":
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("find free TCP port: %v", err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	default:
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("find free UDP port: %v", err)
		}
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).Port
	}
}

func waitForTCP(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("HLS server did not listen on %s", addr)
}

// clipPath returns the committed test clip, see testdata/e2e/README.md.
func clipPath(t *testing.T) string {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", "e2e", "clip.ts"))
	if err != nil {
		t.Fatalf("resolve clip path: %v", err)
	}
	return path
}

// publisher pushes the test clip into the SRT ingest in real time, like OBS would.
type publisher struct {
	done chan struct{}
	err  error
}

// publish starts pushing the clip and stops it when the test ends. The clip's bytes are sent unchanged, paced by the PCR they carry.
func publish(t *testing.T, s *testServer) *publisher {
	t.Helper()

	clip, err := os.ReadFile(clipPath(t))
	if err != nil {
		t.Fatalf("read test clip: %v", err)
	}
	if len(clip)%tsPacketSize != 0 {
		t.Fatalf("test clip is %d bytes, not a whole number of TS packets", len(clip))
	}

	config := gosrt.DefaultConfig()
	config.StreamId = types.ConnectionTypePublisher.String() + ":" + testStreamID
	config.Passphrase = testPassphrase
	conn, err := gosrt.Dial("srt", s.srtAddr, config)
	if err != nil {
		t.Fatalf("dial SRT ingest: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &publisher{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		defer conn.Close()
		// Errors after the test stopped the publisher are just the connection closing
		if err := sendPaced(ctx, conn, clip); err != nil && ctx.Err() == nil {
			p.err = err
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-p.done
	})

	return p
}

// sendPaced writes the TS packets in SRT sized chunks, waiting until each chunk's PCR is due relative to the first PCR.
func sendPaced(ctx context.Context, w io.Writer, clip []byte) error {
	chunkSize := srtChunkPackets * tsPacketSize

	var (
		start    time.Time
		firstPCR int64 = -1
	)
	for off := 0; off < len(clip); off += chunkSize {
		chunk := clip[off:min(off+chunkSize, len(clip))]

		if pcr, ok := firstPCRIn(chunk); ok {
			if firstPCR < 0 {
				firstPCR = pcr
				start = time.Now()
			}
			due := start.Add(time.Duration((pcr - firstPCR) * int64(time.Second) / pcrClockHz))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Until(due)):
			}
		}

		if _, err := w.Write(chunk); err != nil {
			return fmt.Errorf("write to SRT at byte %d: %w", off, err)
		}
	}
	return nil
}

// firstPCRIn returns the first PCR in the 27 MHz clock found in a run of TS packets.
func firstPCRIn(chunk []byte) (int64, bool) {
	for off := 0; off+tsPacketSize <= len(chunk); off += tsPacketSize {
		if pcr, ok := packetPCR(chunk[off : off+tsPacketSize]); ok {
			return pcr, true
		}
	}
	return 0, false
}

func packetPCR(pkt []byte) (int64, bool) {
	hasAdaptation := pkt[3]&0x20 != 0
	if !hasAdaptation || pkt[4] < 7 || pkt[5]&0x10 == 0 {
		return 0, false
	}
	b := pkt[6:12]
	base := int64(b[0])<<25 | int64(b[1])<<17 | int64(b[2])<<9 | int64(b[3])<<1 | int64(b[4])>>7
	ext := int64(b[4]&0x01)<<8 | int64(b[5])
	return base*300 + ext, true
}

// httpGet sends a request like a player would and returns the whole response body.
func httpGet(t *testing.T, method, url string, header http.Header) (*http.Response, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	maps.Copy(req.Header, header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s %s: %v", method, url, err)
	}
	return resp, body
}

// waitForPlaylist polls the playlist until it is served, a player retries the same way after a 503.
func waitForPlaylist(t *testing.T, s *testServer) (*http.Response, []byte) {
	t.Helper()

	deadline := time.Now().Add(playlistReadyTimeout)
	for {
		resp, body := httpGet(t, http.MethodGet, s.playlistURL(), nil)
		if resp.StatusCode == http.StatusOK {
			return resp, body
		}
		if time.Now().After(deadline) {
			t.Fatalf("playlist not ready after %s, last status %d: %s", playlistReadyTimeout, resp.StatusCode, body)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
