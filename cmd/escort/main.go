package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src"
	"github.com/OverlayFox/VRC-Haven/src/dashboard"
	havenPkg "github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/logging"
	"github.com/OverlayFox/VRC-Haven/src/profiling"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
)

var logger zerolog.Logger

func main() {
	config, err := src.LoadConfig(false)
	if err != nil {
		panic(fmt.Errorf("failed to parse config: %w", err))
	}
	if err := config.Validate(); err != nil {
		panic(fmt.Errorf("failed to validate config: %w", err))
	}
	logger = logging.New("escort", config.Log.Level)
	logger.Info().Msg("Starting in Escort mode")

	profiling.Start(logger, config.Profiler)

	ctx, cancel := context.WithCancel(context.Background())
	haven, err := havenPkg.NewHaven(ctx, logger, config.Haven, nil, config.SRT.Passphrase, config.SRT.StreamID)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create haven")
		return
	}

	hlsServer, err := hls.New(ctx, logger, config.HLS, haven, nil)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create HLS server")
		return
	}
	hlsServer.Start()

	srtServer, err := srt.New(ctx, logger, config.SRT, haven, nil)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create SRT server")
		return
	}

	dashboardServer, err := dashboard.New(logger, config.Dashboard,
		dashboard.NewSection("ingest", haven.PublisherStats),
		dashboard.NewSection("hls", hlsServer.Stats),
	)
	if err != nil {
		logger.Panic().Err(err).Msg("Failed to create dashboard server")
		return
	}
	dashboardServer.Start()

	if strings.TrimSpace(config.Haven.PublicAddress) == "" {
		pubAddr, err := havenPkg.GetPublicIP(ctx)
		if err != nil {
			logger.Panic().Err(err).Msg("failed to get public IP address for escort, consider manually populating the `HAVEN_PUBLIC_ADDRESS` field")
		}
		config.Haven.PublicAddress = pubAddr.String()
	}

	flagshipAddr := net.JoinHostPort(config.Haven.FlagshipAddress, strconv.Itoa(config.Haven.FlagshipPort))
	hlsPublicAddr := net.JoinHostPort(config.Haven.PublicAddress, strconv.Itoa(config.Haven.PublicPort))
	streamID := srt.EscortStreamID(haven.GetStreamID(), hlsPublicAddr)
	err = srtServer.Dial(flagshipAddr, streamID, config.SRT.Passphrase)
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
