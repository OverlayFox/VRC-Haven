package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	pyroscope "github.com/grafana/pyroscope-go"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/dashboard"
	"github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/logging"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
)

var logger zerolog.Logger

func startPyroscope(serverAddress string) {
	logger.Info().Str("address", serverAddress).Msg("Starting Pyroscope profiler")
	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "vrc-stream-haven.escort",
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
	logger = logging.New("escort")
	if envErr != nil && !os.IsNotExist(envErr) {
		logger.Panic().Err(envErr).Msg("Error loading .env file")
	}
	logger.Info().Msg("Starting in Escort mode")

	pyroscopeAddr := os.Getenv("PYROSCOPE_SERVER_ADDRESS")
	if pyroscopeAddr != "" {
		startPyroscope(pyroscopeAddr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	haven, err := haven.NewHaven(ctx, logger, nil, "thisisaverysecurepassphrase", "test")
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create haven")
		return
	}

	hlsConf := hls.Config{
		Port:           8555,
		Address:        "0.0.0.0",
		Passphrase:     "thisisaverysecurepassphrase",
		IsFlagship:     false,
		WriteTimeout:   10 * time.Second,
		WriteQueueSize: 8192,
	}
	hlsServer, err := hls.New(ctx, logger, hlsConf, haven, nil)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create HLS server")
		return
	}
	hlsServer.Start()

	srtConfig := srt.Config{
		Address: "0.0.0.0",
		Port:    8891,
	}
	srtServer, err := srt.New(ctx, logger, srtConfig, haven, nil)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create SRT server")
		return
	}

	dashboardConf, err := dashboard.ConfigFromEnv(dashboard.RoleEscort)
	if err != nil {
		logger.Panic().Err(err).Msg("Invalid dashboard configuration")
		return
	}
	dashboardServer, err := dashboard.New(logger, dashboardConf,
		dashboard.NewSection("ingest", haven.PublisherStats),
		dashboard.NewSection("hls", hlsServer.Stats),
	)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create dashboard server")
		return
	}
	dashboardServer.Start()

	// The flagship redirects viewers to this address, a bare ":port" makes it use the IP this escort connects from
	hlsPublicAddr := os.Getenv("HLS_PUBLIC_ADDR")
	if hlsPublicAddr == "" {
		hlsPublicAddr = fmt.Sprintf(":%d", hlsConf.Port)
	}

	err = srtServer.Dial("127.0.0.1:8890", srt.EscortStreamID(haven.GetStreamID(), hlsPublicAddr), "thisisaverysecurepassphrase")
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to dial SRT server")
		return
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

	logger.Info().Msg("Received interrupt signal, shutting down...")
	cancel()
}
