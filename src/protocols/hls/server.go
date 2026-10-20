package hls

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Stream-Haven/src/types"
)

type Server struct {
	logger zerolog.Logger
	config Config

	httpServer *http.Server

	haven   types.Haven
	locator types.Locator

	isFlagship bool

	connection types.ConnectionRTSP
	connMtx    sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(upstreamCtx context.Context, logger zerolog.Logger, config Config, haven types.Haven, locator types.Locator) (types.ProtocolServer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(upstreamCtx)
	s := &Server{
		logger: logger.With().Str("component", "hls").Logger(),
		config: config,

		haven:      haven,
		locator:    locator,
		isFlagship: config.IsFlagship,

		ctx:    ctx,
		cancel: cancel,
	}
	s.httpServer = &http.Server{
		Addr:        fmt.Sprintf("%s:%d", config.Address, config.Port),
		Handler:     s,
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	return s, nil
}

func (s *Server) Dial(address, streamID, passphrase string) error {
	return errors.New("HLS server does not support dialing out to other servers")
}

func (s *Server) Start() {
	s.wg.Go(func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error().Err(err).Msg("HLS server failed to start")
		}
	})
	s.logger.Info().Str("address", s.config.Address).Int("port", s.config.Port).Msg("HLS server started")
}

func (s *Server) Close() {
	s.cancel()

	_ = s.httpServer.Shutdown(context.Background())
	s.wg.Wait()

	s.connMtx.Lock()
	if s.connection != nil {
		s.connection.Close()
	}
	s.connMtx.Unlock()

	s.logger.Info().Msg("HLS server stopped")
}

func (s *Server) validate(path string) (int, error) {
	streamID, passphrase, err := GetCredentials(path)
	if err != nil {
		return http.StatusBadRequest, errors.New("invalid stream path")
	}
	if s.haven.GetStreamID() != streamID {
		return http.StatusNotFound, errors.New("invalid stream ID")
	}
	if s.haven.GetPassphrase() != passphrase {
		return http.StatusUnauthorized, errors.New("invalid passphrase")
	}
	return http.StatusOK, nil
}

// slowSegmentTransfer is the download time above which a segment takes about as long to fetch as it lasts, which drains the player's buffer.
const slowSegmentTransfer = segmentMinDuration

// responseRecorder captures what was sent to a client, so failed transfers can be logged.
type responseRecorder struct {
	http.ResponseWriter

	status   int
	bytes    int
	writeErr error
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	if err != nil && r.writeErr == nil {
		r.writeErr = err
	}
	return n, err
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &responseRecorder{ResponseWriter: w}
	start := time.Now()
	defer func() { s.logRequest(rec, r, time.Since(start)) }()

	s.serveHTTP(rec, r)
}

// logRequest logs a successful response the client did not receive in full or in time, other outcomes are logged where they are decided.
// Only the file name is logged, since the path before it carries the stream's passphrase.
func (s *Server) logRequest(rec *responseRecorder, r *http.Request, duration time.Duration) {
	if rec.status != http.StatusOK && rec.status != http.StatusPartialContent {
		return
	}
	file := ""
	if parts := strings.SplitN(r.URL.Path, "/", 4); len(parts) == 4 {
		file = parts[3]
	}

	var msg string
	contentLength, _ := strconv.Atoi(rec.Header().Get("Content-Length"))
	switch {
	case rec.writeErr != nil || (r.Method != http.MethodHead && rec.bytes < contentLength):
		msg = "HLS response cut short, the client disconnected mid-transfer"
	case strings.HasSuffix(file, ".ts") && duration > slowSegmentTransfer:
		msg = "Slow HLS segment transfer, the client may not keep up with live"
	default:
		return
	}

	event := logClient(s.logger.Warn(), r)
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		event = event.Str("range", rangeHeader)
	}
	if rec.writeErr != nil {
		event = event.AnErr("write_error", rec.writeErr)
	}
	event.Str("method", r.Method).
		Str("file", file).
		Int("status", rec.status).
		Int("bytes", rec.bytes).
		Dur("duration", duration).
		Msg(msg)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Dynamic Origin Echoing (Fixes the wildcard redirect trap)
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	// 2. Allow Credentials & Expose Headers (Required for strict CORS redirects)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type, Date, Server, Transfer-Encoding")

	// 3. Private Network Access
	w.Header().Set("Access-Control-Allow-Private-Network", "true")

	// 4. Handle Preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS, HEAD")

		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		} else {
			w.Header().Set("Access-Control-Allow-Headers", "*")
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	status, err := s.validate(r.URL.Path)
	if err != nil {
		logClient(s.logger.Warn(), r).Err(err).Int("status", status).Msg("HLS request validation failed")
		http.Error(w, err.Error(), status)
		return
	}

	var location types.Location
	if s.isFlagship {
		if s.tryRedirectToEscort(w, r) {
			return
		}
	}

	s.connMtx.Lock()
	if s.connection == nil {
		logClient(s.logger.Info(), r).Msg("First HLS viewer connected, priming stream muxer")
		conn := NewConnection(s.ctx, s.logger, location)
		if err := s.haven.AddConnection(conn); err != nil {
			s.connMtx.Unlock()
			logClient(s.logger.Error(), r).Err(err).Msg("Failed to hook HLS connection to stream haven")
			http.Error(w, "Failed to initialize stream", http.StatusInternalServerError)
			return
		}
		s.connection = conn
		s.wg.Go(func() {
			<-conn.GetCtx().Done()
			s.connMtx.Lock()
			if s.connection == conn {
				s.connection = nil
			}
			s.connMtx.Unlock()
		})
	}
	conn := s.connection
	s.connMtx.Unlock()

	// Push the HLS request down to the connection's playlist/segment handler
	conn.HandleHTTP(w, r)
}

func (s *Server) tryRedirectToEscort(w http.ResponseWriter, r *http.Request) bool {
	addr, err := net.ResolveTCPAddr("tcp", r.RemoteAddr)
	if err != nil {
		logClient(s.logger.Error(), r).Err(err).Msg("Failed to resolve client address")
		http.Error(w, "Failed to resolve client address", http.StatusInternalServerError)
		return true // handled (with error)
	}

	location, err := s.locator.GetLocation(addr)
	if err != nil {
		return false // no redirect, continue locally
	}

	escort := s.haven.GetClosestEscort(location)
	if escort == nil {
		return false // no escort, continue locally
	}

	target := url.URL{
		Scheme:   "http",
		Host:     escort.GetAddr().String(),
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	if strings.HasSuffix(r.URL.Path, "index.m3u8") {
		s.serveManifestRedirect(w, r, &target)
		return true
	}

	// Only the escort is logged, since the redirect path carries the stream's passphrase
	logClient(s.logger.Info(), r).Str("escort", target.Host).Msg("Redirecting HLS request to escort")
	http.Redirect(w, r, target.String(), http.StatusMovedPermanently) // #nosec G710 -- host is the escort's address, not user input
	return true
}

func (s *Server) serveManifestRedirect(w http.ResponseWriter, r *http.Request, target *url.URL) {
	target.Path = r.URL.Path

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.WriteHeader(http.StatusOK)

	targetURL := target.String()
	if !isValidURL(targetURL) {
		logClient(s.logger.Error(), r).Str("escort", target.Host).Msg("Invalid escort redirect URL")
		return
	}

	manifest := fmt.Sprintf("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=5000000\n%s\n", targetURL)
	_, err := w.Write([]byte(manifest)) // #nosec G705 -- URL validated
	if err != nil {
		logClient(s.logger.Error(), r).Err(err).Msg("Failed to write escort redirect manifest")
		return
	}
	logClient(s.logger.Info(), r).Str("escort", target.Host).Msg("Redirected HLS playlist to escort")
}

func isValidURL(urlStr string) bool {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}
