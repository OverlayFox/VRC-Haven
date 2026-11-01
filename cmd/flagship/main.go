package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src"
	"github.com/OverlayFox/VRC-Haven/src/dashboard"
	"github.com/OverlayFox/VRC-Haven/src/geo"
	havenPkg "github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/logging"
	"github.com/OverlayFox/VRC-Haven/src/profiling"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
)

var logger zerolog.Logger

func main() {
	config, err := src.LoadConfig(true)
	if err != nil {
		panic(fmt.Errorf("failed to parse config: %w", err))
	}
	if err := config.Validate(); err != nil {
		panic(fmt.Errorf("failed to validate config: %w", err))
	}

	logger = logging.New("flagship", config.Log.Level)
	logger.Info().Msg("Starting in Flagship mode")

	profiling.Start(logger, config.Profiler)

	ctx, cancel := context.WithCancel(context.Background())
	locator, err := geo.NewLocator(ctx, logger, config.Geo)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create geo locator")
		return
	}

	haven, err := havenPkg.NewHaven(ctx, logger, config.Haven, locator, config.SRT.Passphrase, config.SRT.StreamID)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create haven")
		return
	}

	hlsServer, err := hls.New(ctx, logger, config.HLS, haven, locator)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create HLS server")
		return
	}
	hlsServer.Start()

	srtServer, err := srt.New(ctx, logger, config.SRT, haven, locator)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create SRT server")
		return
	}
	srtServer.Start()

	dashboardServer, err := dashboard.New(logger, config.Dashboard,
		dashboard.NewSection("ingest", haven.PublisherStats),
		dashboard.NewSection("escorts", haven.EscortStats),
		dashboard.NewSection("redirects", hlsServer.RedirectStats),
		dashboard.NewSection("hls", hlsServer.Stats),
	)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create dashboard server")
		return
	}
	dashboardServer.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

	logger.Info().Msg("Received interrupt signal, shutting down...")
	cancel()
}
