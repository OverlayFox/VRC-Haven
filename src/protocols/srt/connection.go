package srt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	goSrt "github.com/datarhei/gosrt"
	"github.com/datarhei/gosrt/packet"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

// writeEOFGrace is how long writes may keep failing with [io.EOF] before the connection counts as closed.
// gosrt also returns [io.EOF] when its write queue is momentarily full, which must not drop the peer.
const writeEOFGrace = time.Second

type connection struct {
	logger zerolog.Logger
	// writeDropLogger is for packets dropped while the write queue is full, which can repeat on every packet
	writeDropLogger zerolog.Logger

	config   goSrt.Config
	server   types.ProtocolServer
	conn     goSrt.Conn
	connType types.ConnectionType
	isEscort bool

	haven          types.Haven
	location       types.Location
	advertisedAddr string

	// writeFailingSince is when writes started failing with io.EOF, zero while they succeed
	writeFailingSince time.Time
	writeDrops        uint64

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func NewConnection(upstreamCtx context.Context, logger zerolog.Logger, haven types.Haven, conn goSrt.Conn, server types.ProtocolServer, config goSrt.Config) (types.ConnectionSRT, error) {
	ctx, cancel := context.WithCancel(upstreamCtx)
	c := &connection{
		logger: logger,

		config:   config,
		server:   server,
		conn:     conn,
		connType: types.ConnectionTypePublishingEscort,
		isEscort: true,

		haven: haven,

		ctx:    ctx,
		cancel: cancel,
	}
	c.writeDropLogger = newWriteDropLogger(c.logger)

	// No waitgroup: this goroutine triggers connection close, avoiding deadlock.
	go func() {
		<-c.ctx.Done()
		c.close()
	}()

	return c, nil
}

func NewConnectionFromRequest(upstreamCtx context.Context, logger zerolog.Logger, haven types.Haven, locator types.Locator, connReq goSrt.ConnRequest) (types.ConnectionSRT, error) {
	c := &connection{
		logger: logger,

		isEscort: false,

		haven: haven,
	}

	streamID, err := parseStreamRequest(connReq)
	if err != nil {
		connReq.Reject(goSrt.REJ_ROGUE)
		return nil, err
	}

	if err = validateConnectionRequest(haven, connReq, streamID); err != nil {
		return nil, err
	}

	c.location, err = locator.GetLocation(connReq.RemoteAddr())
	if err != nil {
		c.logger.Error().Err(err).Msg("failed to get location for new connection")
		c.location = types.Location{}
	}
	c.connType = streamID.connectionType

	if c.connType == types.ConnectionTypeEscort {
		c.advertisedAddr, err = resolveAdvertisedAddr(streamID.advertisedAddr, connReq.RemoteAddr())
		if err != nil {
			connReq.Reject(goSrt.REJ_ROGUE)
			return nil, err
		}
		c.logger = c.logger.With().Str("hls_addr", c.advertisedAddr).Logger()
	}
	c.writeDropLogger = newWriteDropLogger(c.logger)

	c.conn, err = connReq.Accept()
	if err != nil {
		connReq.Reject(goSrt.REJ_PEER)
		return nil, fmt.Errorf("failed to accept SRT connection: %w", err)
	}
	// Created only once accepted, a rejected request would otherwise leave a child context on the server's context
	c.ctx, c.cancel = context.WithCancel(upstreamCtx)

	// No waitgroup: this goroutine triggers connection close, avoiding deadlock.
	go func() {
		<-c.ctx.Done()
		c.close()
	}()

	return c, nil
}

func (c *connection) GetLocation() types.Location {
	return c.location
}

func (c *connection) GetAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *connection) GetType() types.ConnectionType {
	return c.connType
}

func (c *connection) GetAdvertisedAddr() string {
	return c.advertisedAddr
}

func (c *connection) GetStats() types.SRTStats {
	var s goSrt.Statistics
	c.conn.Stats(&s)

	return types.SRTStats{
		ConnectedForMs: s.MsTimeStamp,

		RTTMs:            s.Instantaneous.MsRTT,
		SendMbps:         s.Instantaneous.MbpsSentRate,
		RecvMbps:         s.Instantaneous.MbpsRecvRate,
		LinkCapacityMbps: s.Instantaneous.MbpsLinkCapacity,
		SendLossPercent:  s.Instantaneous.PktSendLossRate,
		RecvLossPercent:  s.Instantaneous.PktRecvLossRate,
		SendBufMs:        s.Instantaneous.MsSendBuf,
		RecvBufMs:        s.Instantaneous.MsRecvBuf,
		LatencyMs:        max(s.Instantaneous.MsSendTsbPdDelay, s.Instantaneous.MsRecvTsbPdDelay),

		PktSent:        s.Accumulated.PktSent,
		PktRecv:        s.Accumulated.PktRecv,
		PktSendLoss:    s.Accumulated.PktSendLoss,
		PktRecvLoss:    s.Accumulated.PktRecvLoss,
		PktRetrans:     s.Accumulated.PktRetrans,
		PktRecvRetrans: s.Accumulated.PktRecvRetrans,
		PktSendDrop:    s.Accumulated.PktSendDrop,
		PktRecvDrop:    s.Accumulated.PktRecvDrop,
		ByteSent:       s.Accumulated.ByteSent,
		ByteRecv:       s.Accumulated.ByteRecv,
	}
}

func (c *connection) GetCtx() context.Context {
	return c.ctx
}

func (c *connection) GetLogger() zerolog.Logger {
	return c.logger
}

// WritePacket is not safe for concurrent use.
func (c *connection) WritePacket(pkt packet.Packet) error {
	select {
	case <-c.ctx.Done():
		return nil // already closing, the haven removes the connection shortly
	default:
	}

	err := c.conn.WritePacket(pkt)
	if err == nil {
		c.writeFailingSince = time.Time{}
		return nil
	}

	if errors.Is(err, io.EOF) {
		// A closed connection fails every write, a full write queue only until it drains
		if c.writeFailingSince.IsZero() {
			c.writeFailingSince = time.Now()
		}
		if time.Since(c.writeFailingSince) < writeEOFGrace {
			c.writeDrops++
			c.writeDropLogger.Warn().Uint64("dropped_total", c.writeDrops).Msg("SRT write queue full, dropping packet")
			return nil
		}
	}

	if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "use of closed network connection") {
		c.logger.Info().Err(err).Msg("SRT connection closed by peer or internally")
		c.Close()
		return err
	}
	c.logger.Error().Err(err).Msg("SRT connection write error")
	c.Close()
	return err
}

func (c *connection) Read() chan packet.Packet {
	pktCh, errCh := c.read()
	c.wg.Go(func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case err := <-errCh:
				if err != nil {
					c.logger.Error().Err(err).Msg("SRT connection read error")
					c.Close()
					return
				}
			}
		}
	})

	return pktCh
}

func (c *connection) Close() {
	c.cancel()
}

func (c *connection) close() {
	err := c.conn.Close()
	if err != nil {
		c.logger.Error().Err(err).Msg("Failed to close SRT connection")
	}
	c.wg.Wait()

	c.logger.Info().Msg("SRT connection closed")
}

func (c *connection) read() (chan packet.Packet, chan error) {
	pktCh := make(chan packet.Packet, 200)
	errCh := make(chan error, 1)

	c.wg.Go(func() {
		defer close(pktCh)
		defer close(errCh)
		waitLogger := c.logger.Sample(&zerolog.BurstSampler{Burst: 1, Period: 5 * time.Second})

		for {
			if c.ctx.Err() != nil {
				return
			}
			if err := c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				select {
				case <-c.ctx.Done():
					return
				case errCh <- fmt.Errorf("failed to set read deadline: %w", err):
				}
				continue
			}

			pkt, err := c.conn.ReadPacket()
			if err != nil {
				var netErr net.Error
				switch {
				case errors.Is(err, io.EOF), strings.Contains(err.Error(), "use of closed network connection"):
					if !c.isEscort {
						c.logger.Info().Err(err).Msg("SRT connection closed by peer or internally")
					} else {
						c.logger.Info().Err(err).Msg("SRT connection to flagship closed, reconnecting")
						go func() {
							_ = c.server.Dial(c.GetAddr().String(), c.config.StreamId, c.haven.GetPassphrase())
						}()
					}
					c.Close()
					return

				case errors.As(err, &netErr) && netErr.Timeout():
					c.logger.Debug().Msg("SRT read deadline exceeded, retrying")
					continue

				default:
					select {
					case errCh <- fmt.Errorf("failed to read packet: %w", err):
					default:
					}
					continue
				}
			}

			select {
			case <-c.ctx.Done():
				return
			case pktCh <- pkt:
				continue
			default:
			}

			// Wait instead of dropping, gosrt's read queue holds the rest of a burst while the consumer catches up
			waitLogger.Debug().Msg("SRT packet channel full, waiting for consumer")
			select {
			case <-c.ctx.Done():
				return
			case pktCh <- pkt:
			}
		}
	})

	return pktCh, errCh
}

func newWriteDropLogger(logger zerolog.Logger) zerolog.Logger {
	return logger.Sample(&zerolog.BurstSampler{Burst: 1, Period: 5 * time.Second})
}
