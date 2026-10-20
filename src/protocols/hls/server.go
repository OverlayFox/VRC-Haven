package hls

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
		logger: logger,
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
		// Players poll every target duration, keeping their connections open avoids a
		// fresh TCP handshake and slow start per request for distant viewers.
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
	s.logger.Info().Msgf("HLS server started on %s:%d", s.config.Address, s.config.Port)
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

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		s.logger.Info().Err(err).Str("client_ip", r.RemoteAddr).Msg("HLS request validation failed")
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
		s.logger.Info().Str("client_ip", r.RemoteAddr).Msg("First LL-HLS viewer connected, priming stream muxer...")
		conn := NewConnection(s.ctx, s.logger, location)
		if err := s.haven.AddConnection(conn); err != nil {
			s.connMtx.Unlock()
			s.logger.Error().Err(err).Str("client_ip", r.RemoteAddr).Msg("Failed to hook HLS connection to stream haven")
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
		s.logger.Error().Err(err).Str("client_ip", r.RemoteAddr).Msg("Failed to resolve client address")
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

	s.logger.Info().Str("client_ip", r.RemoteAddr).Str("redirect_uri", target.String()).Msg("Redirecting stray HLS request")
	http.Redirect(w, r, target.String(), http.StatusMovedPermanently) // #nosec G710 -- host is the escort's address, not user input
	return true
}

func (s *Server) serveManifestRedirect(w http.ResponseWriter, r *http.Request, target *url.URL) {
	target.Path = r.URL.Path

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.WriteHeader(http.StatusOK)

	targetURL := target.String()
	if !isValidURL(targetURL) {
		s.logger.Error().Str("client_ip", r.RemoteAddr).Str("url", targetURL).Msg("Invalid redirect URL")
		return
	}

	manifest := fmt.Sprintf("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=5000000\n%s\n", targetURL)
	_, err := w.Write([]byte(manifest)) // #nosec G705 -- URL validated
	if err != nil {
		s.logger.Error().Err(err).Str("client_ip", r.RemoteAddr).Msg("Failed to write manifest response")
		return
	}
	s.logger.Info().Str("client_ip", r.RemoteAddr).Str("target", targetURL).Msg("Served absolute manifest redirect")
}

func isValidURL(urlStr string) bool {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}
