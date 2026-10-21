package haven

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/datarhei/gosrt/packet"
	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/buffer"
	"github.com/OverlayFox/VRC-Haven/src/multiplexer"
	"github.com/OverlayFox/VRC-Haven/src/types"
)

// demuxerInputCap is how many SRT packets may queue for the demuxer.
const demuxerInputCap = 2048

type Haven struct {
	logger zerolog.Logger

	streamID   string
	passphrase string

	publisher types.ConnectionSRT               // publisher provides the main stream
	escorts   []types.ConnectionSRT             // escorts are nodes that relay the publisher's stream to viewers
	viewers   map[net.Addr]types.ConnectionRTSP // viewers are connections that only consume the stream

	publisherMtx sync.RWMutex
	escortMtx    sync.RWMutex
	viewersMtx   sync.RWMutex

	buffer  types.Buffer
	demuxer *multiplexer.MpegTsDemuxer

	locator types.Locator

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func NewHaven(upstreamCtx context.Context, logger zerolog.Logger, locator types.Locator, passphrase, streamID string) (types.Haven, error) {
	ctx, cancel := context.WithCancel(upstreamCtx)
	return &Haven{
		logger: logger.With().Str("component", "haven").Logger(),

		streamID:   streamID,
		passphrase: passphrase,

		publisher: nil,
		escorts:   make([]types.ConnectionSRT, 0),
		viewers:   make(map[net.Addr]types.ConnectionRTSP),

		locator: locator,

		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (h *Haven) GetStreamID() string {
	return h.streamID
}

func (h *Haven) GetPassphrase() string {
	return h.passphrase
}

func (h *Haven) GetPublisher() (types.ConnectionSRT, error) {
	h.publisherMtx.RLock()
	defer h.publisherMtx.RUnlock()

	if h.publisher == nil {
		return nil, types.ErrPublisherNotFound
	}
	return h.publisher, nil
}

func (h *Haven) GetClosestEscort(location types.Location) types.ConnectionSRT {
	h.publisherMtx.RLock()
	if h.publisher == nil {
		h.publisherMtx.RUnlock()
		return nil
	}
	flagshipLocation := h.publisher.GetLocation() // TODO: use the havens location instead of the publishers location
	h.publisherMtx.RUnlock()

	h.escortMtx.RLock()
	defer h.escortMtx.RUnlock()

	var closestEscort types.ConnectionSRT
	clientGeoPoint := location.GetGeoPoint()
	closestDistance := flagshipLocation.GetDistanceBetween(clientGeoPoint)

	for _, escort := range h.escorts {
		escortLocation := escort.GetLocation()
		distance := escortLocation.GetDistanceBetween(clientGeoPoint)

		if distance < closestDistance {
			closestDistance = distance
			closestEscort = escort
		}
	}

	return closestEscort
}

func (h *Haven) GetViewer(conn net.Addr) (types.ConnectionRTSP, error) {
	h.viewersMtx.RLock()
	defer h.viewersMtx.RUnlock()

	viewer, ok := h.viewers[conn]
	if !ok {
		return nil, types.ErrViewerNotFound
	}
	return viewer, nil
}

func (h *Haven) AddConnection(conn types.Connection) error {
	switch conn.GetType() {
	case types.ConnectionTypeEscort:
		conn, ok := conn.(types.ConnectionSRT)
		if !ok {
			return errors.New("invalid connection type for escort")
		}
		return h.addEscort(conn)
	case types.ConnectionTypePublisher, types.ConnectionTypePublishingEscort:
		conn, ok := conn.(types.ConnectionSRT)
		if !ok {
			return errors.New("invalid connection type for publisher")
		}
		return h.addPublisher(conn)
	case types.ConnectionTypeReader:
		conn, ok := conn.(types.ConnectionRTSP)
		if !ok {
			return errors.New("invalid connection type for viewer")
		}
		return h.addViewer(conn)
	default:
		return errors.New("unknown connection type")
	}
}

func (h *Haven) addEscort(conn types.ConnectionSRT) error {
	h.escortMtx.Lock()
	h.escorts = append(h.escorts, conn)
	h.escortMtx.Unlock()

	// Monitor the escort's context and remove it from the map when it is done
	h.wg.Go(func() {
		select {
		case <-h.ctx.Done():
			h.logger.Debug().Str("remote_addr", conn.GetAddr().String()).Msg("Haven context done, closing escort")
			conn.Close()

		case <-conn.GetCtx().Done():
			h.logger.Debug().Str("remote_addr", conn.GetAddr().String()).Msg("Escort context done, removing escort from haven")

			h.escortMtx.Lock()
			defer h.escortMtx.Unlock()
			for i, e := range h.escorts {
				if e == conn {
					h.escorts = append(h.escorts[:i], h.escorts[i+1:]...)
					break
				}
			}
		}
	})

	return nil
}

func (h *Haven) addViewer(conn types.ConnectionRTSP) error {
	if !h.PublisherIsReady() {
		return types.ErrPublisherNotFound
	}

	h.viewersMtx.Lock()
	h.viewers[conn.GetAddr()] = conn
	h.viewersMtx.Unlock()

	viewerLogger := conn.GetLogger()
	viewerLogger.Debug().Msg("Added viewer to haven")

	bufferOutput, err := h.buffer.Subscribe(conn.GetCtx(), -2*time.Second)
	if err != nil {
		viewerLogger.Error().Err(err).Msg("Failed to subscribe viewer to buffer")
		return err
	}

	err = conn.Write(bufferOutput)
	if err != nil {
		viewerLogger.Error().Err(err).Msg("Failed to write buffer output to viewer")
		return err
	}

	// Monitor the viewer's context and remove it from the map when it is done
	h.wg.Go(func() {
		select {
		case <-h.ctx.Done():
			viewerLogger.Debug().Msg("Haven context done, closing viewer")
			conn.Close()

		case <-conn.GetCtx().Done():
			viewerLogger.Debug().Msg("Viewer context done, removing viewer from haven")

			h.viewersMtx.Lock()
			defer h.viewersMtx.Unlock()
			for addr, v := range h.viewers {
				if v == conn {
					delete(h.viewers, addr)
					break
				}
			}
		}
	})

	return nil
}

func (h *Haven) addPublisher(conn types.ConnectionSRT) error {
	h.publisherMtx.Lock()
	defer h.publisherMtx.Unlock()

	if h.publisher != nil {
		return types.ErrPublisherAlreadyExists
	}
	h.publisher = conn
	h.initPublisher()

	// Monitor the flagship's context and remove it from the map when it is done
	// Closes the escorts as well
	h.wg.Go(func() {
		select {
		case <-h.ctx.Done():
			h.logger.Debug().Msg("Haven context done, closing publisher")
			h.publisher.Close()

		case <-h.publisher.GetCtx().Done():
			h.logger.Debug().Msg("Publisher context done, removing publisher from haven")
			h.clearPublisher()
		}
	})
	h.readFromPublisher()

	return nil
}

// initPublisher resets the buffer and demuxer for a new publisher.
func (h *Haven) initPublisher() {
	publisherLogger := h.publisher.GetLogger()

	if h.buffer != nil {
		h.buffer.Close()
		h.buffer = nil
	}
	h.buffer = buffer.NewBuffer(publisherLogger.With().Str("module", "buffer").Logger())

	if h.demuxer != nil {
		h.demuxer.Close()
		h.demuxer = nil
	}
	h.demuxer = multiplexer.NewMpegTsDemuxer(h.publisher.GetCtx(), publisherLogger.With().Str("module", "demuxer").Logger(), multiplexer.Settings{
		InputBufferCap:  50,
		OutputBufferCap: 200,
		AudioDriftLimit: 20 * time.Millisecond,
	})
}

func (h *Haven) clearPublisher() {
	h.publisherMtx.Lock()
	defer h.publisherMtx.Unlock()

	h.escortMtx.Lock()
	for _, escort := range h.escorts {
		h.logger.Info().Str("remote_addr", escort.GetAddr().String()).Msg("Closing escort as publisher has disconnected")
		escort.Close()
	}
	h.escorts = make([]types.ConnectionSRT, 0)
	h.escortMtx.Unlock()

	h.viewersMtx.Lock()
	for addr, viewer := range h.viewers {
		viewerLogger := viewer.GetLogger()
		viewerLogger.Info().Msg("Closing viewer as publisher has disconnected")
		viewer.Close()
		delete(h.viewers, addr)
	}
	h.viewersMtx.Unlock()

	h.publisher = nil

	h.buffer.Close()
	h.buffer = nil

	h.demuxer.Close()
	h.demuxer = nil
}

func (h *Haven) broadcastToEscorts(packet packet.Packet) []error {
	h.escortMtx.RLock()
	defer h.escortMtx.RUnlock()

	var errs []error
	for _, escort := range h.escorts {
		escortClone := packet.Clone()
		err := escort.WritePacket(escortClone)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (h *Haven) demuxerToBufferWrite(demuxerEgress <-chan types.Frame, demuxerErrCh <-chan error) {
	h.wg.Go(func() {
		for {
			select {
			case <-h.ctx.Done():
				h.logger.Debug().Msg("Haven context done, closing demuxer")
				return

			case err := <-demuxerErrCh:
				if err != nil {
					h.logger.Error().Err(err).Msg("Demuxer error")
				}
				return

			case frame, ok := <-demuxerEgress:
				if !ok {
					h.logger.Debug().Msg("Demuxer egress channel closed, stopping write to buffer")
					return
				}

				err := h.buffer.Write(frame.Clone())
				if err != nil {
					h.logger.Error().Err(err).Msg("Demuxer egress to buffer write error")
				}
				frame.Decommission()
			}
		}
	})
}

func (h *Haven) readFromPublisher() {
	h.wg.Go(func() {
		demuxerPktCh := make(chan packet.Packet, demuxerInputCap)
		h.demuxerToBufferWrite(h.demuxer.StartDemuxer(demuxerPktCh))

		droppedLogger := h.logger.Sample(&zerolog.BurstSampler{Burst: 1, Period: 5 * time.Second})
		var droppedPackets uint64

		publisherEgress := h.publisher.Read()
		for {
			select {
			case <-h.ctx.Done():
				return
			case p, ok := <-publisherEgress:
				if !ok {
					h.logger.Debug().Msg("Packet channel closed")
					return
				}
				demuxerClone := p.Clone()
				select {
				case demuxerPktCh <- demuxerClone:
				default:
					demuxerClone.Decommission()
					droppedPackets++
					droppedLogger.Warn().Uint64("dropped_total", droppedPackets).Msg("Demuxer input full, dropping SRT packet")
				}

				broadcastToEscortsErrs := h.broadcastToEscorts(p)
				for _, err := range broadcastToEscortsErrs {
					h.logger.Error().Err(err).Msg("Error broadcasting packet to escort")
				}
				p.Decommission()
			}
		}
	})
}

func (h *Haven) Close() {
	h.logger.Debug().Msg("Closing Haven")

	h.cancel()
	h.wg.Wait()

	h.publisherMtx.Lock()
	defer h.publisherMtx.Unlock()
	if h.publisher != nil {
		h.publisher.Close()
		h.publisher = nil
	}

	h.escortMtx.Lock()
	defer h.escortMtx.Unlock()
	for _, escort := range h.escorts {
		escort.Close()
	}
	h.escorts = make([]types.ConnectionSRT, 0)

	h.viewersMtx.Lock()
	defer h.viewersMtx.Unlock()
	for _, viewer := range h.viewers {
		viewer.Close()
	}
	h.viewers = make(map[net.Addr]types.ConnectionRTSP)

	if h.demuxer != nil {
		h.demuxer.Close()
		h.demuxer = nil
	}

	if h.buffer != nil {
		h.buffer.Close()
		h.buffer = nil
	}
}

func (h *Haven) PublisherStats() *types.PeerStats {
	h.publisherMtx.RLock()
	defer h.publisherMtx.RUnlock()

	if h.publisher == nil {
		return nil
	}
	stats := peerStats(h.publisher)
	return &stats
}

func (h *Haven) EscortStats() []types.PeerStats {
	h.escortMtx.RLock()
	defer h.escortMtx.RUnlock()

	stats := make([]types.PeerStats, 0, len(h.escorts))
	for _, escort := range h.escorts {
		stats = append(stats, peerStats(escort))
	}
	return stats
}

//
// Helper functions
//

func peerStats(conn types.ConnectionSRT) types.PeerStats {
	return types.PeerStats{
		Addr:     conn.GetAddr().String(),
		Type:     conn.GetType(),
		Location: conn.GetLocation(),
		HLSAddr:  conn.GetAdvertisedAddr(),
		SRT:      conn.GetStats(),
	}
}

func (h *Haven) PublisherIsReady() bool {
	h.publisherMtx.RLock()
	defer h.publisherMtx.RUnlock()

	return h.publisher != nil
}
