package main

import (
	"context"
	"os"
	"os/signal"
	"time"

	pyroscope "github.com/grafana/pyroscope-go"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/geo"
	"github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/logging"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
)

var logger zerolog.Logger

func startPyroscope(serverAddress string) {
	logger.Info().Str("address", serverAddress).Msg("Starting Pyroscope profiler")
	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "vrc-stream-haven.flagship",
		ServerAddress:   serverAddress,
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
		},
	})
	if err != nil {
		logger.Error().Err(err).Msg("Failed to start Pyroscope profiler")
	}
}

func main() {
	envErr := godotenv.Load()
	logger = logging.New("flagship")
	if envErr != nil && !os.IsNotExist(envErr) {
		logger.Panic().Err(envErr).Msg("Error loading .env file")
	}
	logger.Info().Msg("Starting in Flagship mode")

	pyroscopeAddr := os.Getenv("PYROSCOPE_SERVER_ADDRESS")
	if pyroscopeAddr != "" {
		startPyroscope(pyroscopeAddr)
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

	haven, err := haven.NewHaven(ctx, logger, locator, "thisisaverysecurepassphrase", "test")
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create haven")
		return
	}

	hlsConf := hls.Config{
		Port:           8554,
		Address:        "0.0.0.0",
		Passphrase:     "thisisaverysecurepassphrase",
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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

	logger.Info().Msg("Received interrupt signal, shutting down...")
	cancel()
}
