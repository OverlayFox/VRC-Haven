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

	"github.com/OverlayFox/VRC-Haven/src/multiplexer"
	"github.com/OverlayFox/VRC-Haven/src/types"
)

type connection struct {
	logger zerolog.Logger

	config   goSrt.Config
	server   types.ProtocolServer
	conn     goSrt.Conn
	connType types.ConnectionType
	isEscort bool

	demuxer *multiplexer.MpegTsDemuxer

	haven          types.Haven
	location       types.Location
	advertisedAddr string

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func NewConnection(upstreamCtx context.Context, logger zerolog.Logger, haven types.Haven, locator types.Locator, conn goSrt.Conn, server types.ProtocolServer, config goSrt.Config) (types.ConnectionSRT, error) {
	ctx, cancel := context.WithCancel(upstreamCtx)
	c := &connection{
		logger: logger,

		config:   config,
		server:   server,
		conn:     conn,
		connType: types.ConnectionTypePublishingEscort,
		isEscort: true,

		demuxer: multiplexer.NewMpegTsDemuxer(ctx, logger.With().Str("module", "demuxer").Logger(), multiplexer.Settings{
			InputBufferCap:  50,
			OutputBufferCap: 200,
			AudioDriftLimit: 20 * time.Millisecond,
		}),

		haven: haven,

		ctx:    ctx,
		cancel: cancel,
	}

	return c, nil
}

func NewConnectionFromRequest(upstreamCtx context.Context, logger zerolog.Logger, haven types.Haven, locator types.Locator, connReq goSrt.ConnRequest) (types.ConnectionSRT, error) {
	ctx, cancel := context.WithCancel(upstreamCtx)
	c := &connection{
		logger: logger,

		isEscort: false,

		demuxer: multiplexer.NewMpegTsDemuxer(ctx, logger.With().Str("module", "demuxer").Logger(), multiplexer.Settings{
			InputBufferCap:  50,
			OutputBufferCap: 200,
			AudioDriftLimit: 20 * time.Millisecond,
		}),

		haven: haven,

		ctx:    ctx,
		cancel: cancel,
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

	c.conn, err = connReq.Accept()
	if err != nil {
		connReq.Reject(goSrt.REJ_PEER)
		return nil, fmt.Errorf("failed to accept SRT connection: %w", err)
	}

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

func (c *connection) WritePacket(pkt packet.Packet) error {
	err := c.conn.WritePacket(pkt)
	if err != nil {
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "use of closed network connection") {
			c.logger.Info().Err(err).Msg("SRT connection closed by peer or internally")
			c.Close()
			return err
		}
		c.logger.Error().Err(err).Msg("SRT connection write error")
		c.Close()
		return err
	}

	return nil
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

		dropLogger := c.logger.Sample(&zerolog.BurstSampler{Burst: 1, Period: 5 * time.Second})
		var dropped uint64

		for {
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
			default:
				select {
				case <-c.ctx.Done():
					return
				default:
					dropped++
					dropLogger.Warn().Uint64("dropped_total", dropped).Msg("SRT packet channel full, dropping packet")
				}
				continue
			}
		}
	})

	return pktCh, errCh
}
