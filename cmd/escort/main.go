package main

import (
	"context"
	"os"
	"os/signal"
	"time"

	pyroscope "github.com/grafana/pyroscope-go"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Stream-Haven/src/haven"
	"github.com/OverlayFox/VRC-Stream-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Stream-Haven/src/protocols/srt"
)

var logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "15:04:05"}).With().Str("component", "escort").Timestamp().Logger()

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

func loadEnv() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		logger.Panic().Err(err).Msg("Error loading .env file")
	}
}

func main() {
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	logger.Info().Msg("Starting in Escort mode")
	loadEnv()

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

	err = srtServer.Dial("127.0.0.1:8890", "escort:ingest", "thisisaverysecurepassphrase")
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
