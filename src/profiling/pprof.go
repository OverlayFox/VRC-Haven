// Package profiling serves the Go runtime profiles for `go tool pprof`.
package profiling

import (
	"errors"
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// StartFromEnv serves the profiles on PPROF_ADDR (e.g. localhost:6060) under /debug/pprof/, it does nothing if PPROF_ADDR is empty.
// The profiles expose stack traces and memory contents, so keep the address off public interfaces.
func StartFromEnv(logger zerolog.Logger) {
	address := os.Getenv("PPROF_ADDR")
	if address == "" {
		return
	}
	logger = logger.With().Str("component", "pprof").Logger()

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// No write timeout, CPU profiles and traces stream for as long as ?seconds= asks
	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("pprof server failed")
		}
	}()
	logger.Info().Str("address", address).Msg("pprof server started")
}
