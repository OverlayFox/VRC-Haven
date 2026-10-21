package main

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/dashboard"
	"github.com/OverlayFox/VRC-Haven/src/geo"
	"github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/logging"
	"github.com/OverlayFox/VRC-Haven/src/profiling"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
)

var logger zerolog.Logger

func main() {
	envErr := godotenv.Load()
	logger = logging.New("flagship")
	if envErr != nil && !os.IsNotExist(envErr) {
		logger.Panic().Err(envErr).Msg("Error loading .env file")
	}
	logger.Info().Msg("Starting in Flagship mode")

	profiling.StartFromEnv(logger)

	// Escorts and viewers need both, they are part of the SRT stream ID and the HLS path
	passphrase := os.Getenv("HAVEN_PASSPHRASE")
	if passphrase == "" {
		passphrase = "thisisaverysecurepassphrase"
	}
	streamID := os.Getenv("HAVEN_STREAM_ID")
	if streamID == "" {
		streamID = "test"
	}

	ctx, cancel := context.WithCancel(context.Background())
	geoConf := geo.Config{
		LicenseKey: os.Getenv("MAX_MIND_LICENSE_KEY"),
		AccountID:  os.Getenv("MAX_MIND_ACCOUNT_ID"),
		Dir:        "./GeoDatabase",
	}
	locator, err := geo.NewLocator(ctx, logger, geoConf)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create geo locator")
		return
	}

	haven, err := haven.NewHaven(ctx, logger, locator, passphrase, streamID)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create haven")
		return
	}

	hlsConf := hls.Config{
		Port:           8554,
		Address:        "0.0.0.0",
		Passphrase:     passphrase,
		IsFlagship:     true,
		WriteTimeout:   10 * time.Second,
		WriteQueueSize: 8192,
	}
	hlsServer, err := hls.New(ctx, logger, hlsConf, haven, locator)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create HLS server")
		return
	}
	hlsServer.Start()

	srtConfig := srt.Config{
		Address: "0.0.0.0",
		Port:    8890,
	}
	srtServer, err := srt.New(ctx, logger, srtConfig, haven, locator)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create SRT server")
		return
	}
	srtServer.Start()

	dashboardConf, err := dashboard.ConfigFromEnv(dashboard.RoleFlagship)
	if err != nil {
		logger.Panic().Err(err).Msg("Invalid dashboard configuration")
		return
	}
	dashboardServer, err := dashboard.New(logger, dashboardConf,
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
