package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/dashboard"
	"github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/logging"
	"github.com/OverlayFox/VRC-Haven/src/profiling"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
)

var logger zerolog.Logger

func main() {
	envErr := godotenv.Load()
	logger = logging.New("escort")
	if envErr != nil && !os.IsNotExist(envErr) {
		logger.Panic().Err(envErr).Msg("Error loading .env file")
	}
	logger.Info().Msg("Starting in Escort mode")

	profiling.StartFromEnv(logger)

	// Both have to match the flagship, they are part of the SRT stream ID and the HLS path viewers are redirected to
	passphrase := os.Getenv("HAVEN_PASSPHRASE")
	if passphrase == "" {
		passphrase = "thisisaverysecurepassphrase"
	}
	streamID := os.Getenv("HAVEN_STREAM_ID")
	if streamID == "" {
		streamID = "test"
	}

	hlsPort := 8555
	if port := os.Getenv("HLS_PORT"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			logger.Panic().Err(err).Msgf("Invalid HLS_PORT '%s'", port)
			return
		}
		hlsPort = p
	}

	ctx, cancel := context.WithCancel(context.Background())
	haven, err := haven.NewHaven(ctx, logger, nil, passphrase, streamID)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create haven")
		return
	}

	hlsConf := hls.Config{
		Port:           hlsPort,
		Address:        "0.0.0.0",
		Passphrase:     passphrase,
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

	// SRT address of the flagship to pull the stream from
	flagshipAddr := os.Getenv("FLAGSHIP_SRT_ADDR")
	if flagshipAddr == "" {
		flagshipAddr = "127.0.0.1:8890"
	}

	err = srtServer.Dial(flagshipAddr, srt.EscortStreamID(haven.GetStreamID(), hlsPublicAddr), passphrase)
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
