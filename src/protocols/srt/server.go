package srt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	gosrt "github.com/datarhei/gosrt"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

const (
	initialBackoff    = 100 * time.Millisecond
	maxBackoff        = 30 * time.Second
	backoffMultiplier = 2.0
)

type Config struct {
	Address string
	Port    int
}

type server struct {
	logger zerolog.Logger
	config Config

	haven   types.Haven
	locator types.Locator

	listener gosrt.Listener

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func New(upstreamCtx context.Context, logger zerolog.Logger, config Config, haven types.Haven, locator types.Locator) (types.ProtocolServer, error) {
	listener, err := gosrt.Listen("srt", fmt.Sprintf("%s:%d", config.Address, config.Port), gosrt.DefaultConfig()) // TODO: Optimise DefaultConfig() for large WAN hops
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(upstreamCtx)
	s := &server{
		logger: logger.With().Str("component", "srt").Logger(),
		config: config,

		haven:   haven,
		locator: locator,

		listener: listener,

		ctx:    ctx,
		cancel: cancel,
	}
	return s, nil
}

func (s *server) Start() {
	acceptCh := make(chan gosrt.ConnRequest, 10)
	errCh := make(chan error, 1)

	// start listener
	s.wg.Go(func() {
		for {
			req, err := s.listener.Accept2()
			if err != nil {
				if errors.Is(err, gosrt.ErrListenerClosed) {
					s.logger.Info().Msg("SRT listener closed")
					return
				}
				errCh <- err
				return
			}
			acceptCh <- req
		}
	})
	s.logger.Info().Str("address", s.config.Address).Int("port", s.config.Port).Msg("Started SRT listener")

	// handle incoming connections
	s.wg.Go(func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			case err := <-errCh:
				s.logger.Error().Err(err).Msg("SRT listener error on connection accept")
			case req := <-acceptCh:
				connectionLogger := s.logger.With().Str("remote_addr", req.RemoteAddr().String()).Str("stream_id", req.StreamId()).Logger()
				connectionLogger.Info().Msg("Accepted new SRT connection")

				go func() {
					conn, err := NewConnectionFromRequest(s.ctx, connectionLogger, s.haven, s.locator, req)
					if err != nil {
						connectionLogger.Error().Err(err).Msg("Failed to handle SRT connection")
						return
					}
					if err := s.haven.AddConnection(conn); err != nil {
						connectionLogger.Error().Err(err).Msg("Failed to add SRT connection to haven")
						conn.Close()
						return
					}
					connectionLogger.Info().Msg("SRT connection added to haven")
				}()
			}
		}
	})
}

func (s *server) Dial(address string, streamID, passphrase string) error {
	config := gosrt.DefaultConfig()
	if streamID != "" {
		config.StreamId = streamID
	}
	if passphrase != "" {
		config.Passphrase = passphrase
	}
	s.logger.Info().Str("remote_addr", address).Str("stream_id", streamID).Msg("Dialing remote Flagship SRT server")

	var conn gosrt.Conn
	tries := 0
	backoff := initialBackoff
	for {
		var err error
		conn, err = gosrt.Dial("srt", address, config)
		if err == nil {
			break
		}
		tries++
		s.logger.Warn().Err(err).Int("attempt", tries).Int64("backoff_ms", backoff.Milliseconds()).Msg("Failed to establish initial connection with Flagship SRT server, retrying")
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(backoff):
			continue
		}
	}

	connection, err := NewConnection(s.ctx, s.logger, s.haven, s.locator, conn, s, config)
	if err != nil {
		err = conn.Close()
		if err != nil {
			return fmt.Errorf("failed to close SRT connection after dial failure: %w", err)
		}
		return fmt.Errorf("failed to create SRT connection: %w", err)
	}

	if err := s.haven.AddConnection(connection); err != nil {
		connection.Close()
		return fmt.Errorf("failed to add SRT connection to haven: %w", err)
	}
	s.logger.Info().Str("remote_addr", address).Msg("Successfully connected to remote Flagship SRT server")

	return nil
}

func (s *server) Close() {
	s.listener.Close()
	s.cancel()
	s.wg.Wait()

	s.logger.Info().Msg("SRT server closed")
}
