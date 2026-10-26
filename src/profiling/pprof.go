// Package profiling serves the Go runtime profiles for `go tool pprof`.
package profiling

import (
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

// Start serves the profiles on PPROF_ADDR (e.g. localhost:6060) under /debug/pprof/, it does nothing if PPROF_ADDR is empty.
// The profiles expose stack traces and memory contents, so keep the address off public interfaces.
func Start(logger zerolog.Logger, config Config) {
	if config.Address == "" {
		return
	}
	logger = logger.With().Str("component", "pprof").Logger()

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	addr := net.JoinHostPort(config.Address, strconv.Itoa(config.Port))
	// No write timeout, CPU profiles and traces stream for as long as ?seconds= asks
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("pprof server failed")
		}
	}()
	logger.Info().Str("address", addr).Msg("pprof server started")
}
