package multiplexer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/asticode/go-astits"
	"github.com/datarhei/gosrt/packet"
	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Haven/src/buffer"
	"github.com/OverlayFox/VRC-Haven/src/types"
)

const (
	maxTsClock    int64 = 1 << 33
	MpegTsPktSize       = 188
)

type Settings struct {
	InputBufferCap  int
	OutputBufferCap int
	AudioDriftLimit time.Duration
}

type MpegTsDemuxer struct {
	logger   zerolog.Logger
	settings Settings

	astDemux  *astits.Demuxer
	aacFrames *AACFrameSplitter
	buffer    *syncBuffer

	prevPts int64
	prevDts int64
	ptsSet  bool
	dtsSet  bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewMpegTsDemuxer(upstreamCtx context.Context, logger zerolog.Logger, settings Settings) *MpegTsDemuxer {
	ctx, cancel := context.WithCancel(upstreamCtx)
	demux := &MpegTsDemuxer{
		logger:   logger,
		settings: settings,

		buffer:    NewSyncBuffer(settings.InputBufferCap),
		aacFrames: NewAACFrameSplitter(logger, settings.AudioDriftLimit),

		ctx:    ctx,
		cancel: cancel,
	}
	demux.astDemux = astits.NewDemuxer(ctx, demux)
	return demux
}

// Read Implements [io.Reader].
func (d *MpegTsDemuxer) Read(b []byte) (int, error) {
	n, err := d.buffer.read(b)
	if err != nil && !errors.Is(err, io.EOF) && d.ctx.Err() != nil {
		return n, d.ctx.Err()
	}
	return n, err
}

func (d *MpegTsDemuxer) write(pkt packet.Packet) error {
	defer pkt.Decommission()

	raw := pkt.Data()
	if len(raw)%MpegTsPktSize != 0 {
		return fmt.Errorf("bad TS packet size: %d", len(raw))
	}
	for i := 0; i < len(raw); i += MpegTsPktSize {
		if raw[i] != 0x47 {
			return fmt.Errorf("ts sync byte error at %d: got 0x%02X", i, raw[i])
		}
	}
	if err := d.buffer.write(raw); err != nil {
		return fmt.Errorf("buffer write failed: %w", err)
	}
	return nil
}

func (d *MpegTsDemuxer) StartDemuxer(pktChan chan packet.Packet) (chan types.Frame, chan error) {
	frameChan := make(chan types.Frame, d.settings.OutputBufferCap)
	errChan := make(chan error, 1)

	d.wg.Go(func() {
		for {
			select {
			case <-d.ctx.Done():
				return
			case pkt, ok := <-pktChan:
				if !ok {
					return
				}
				if err := d.write(pkt); err != nil {
					select {
					case errChan <- fmt.Errorf("packet write error: %w", err):
					default:
					}
					return
				}
			}
		}
	})

	d.wg.Go(func() {
		defer close(frameChan)
		pidTypes := make(map[uint16]astits.StreamType)
		for {
			select {
			case <-d.ctx.Done():
				return
			default:
				if err := d.handleNext(frameChan, pidTypes); err != nil {
					d.buffer.setError(err)
					d.logger.Error().Err(err).Msg("Demuxer error")
					return
				}
			}
		}
	})

	return frameChan, errChan
}

func (d *MpegTsDemuxer) handleNext(frameChan chan types.Frame, pidTypes map[uint16]astits.StreamType) error {
	data, err := d.astDemux.NextData()
	if err != nil {
		return fmt.Errorf("next data error: %w", err)
	}
	d.updatePidTypes(data, pidTypes)
	return d.processPES(data, frameChan, pidTypes)
}

func (d *MpegTsDemuxer) updatePidTypes(data *astits.DemuxerData, pidTypes map[uint16]astits.StreamType) {
	if data.PMT == nil {
		return
	}
	for _, es := range data.PMT.ElementaryStreams {
		if _, ok := pidTypes[es.ElementaryPID]; !ok {
			pidTypes[es.ElementaryPID] = es.StreamType
		}
	}
}

func (d *MpegTsDemuxer) processPES(data *astits.DemuxerData, frameChan chan types.Frame, pidTypes map[uint16]astits.StreamType) error {
	if data.PES == nil || len(data.PES.Data) == 0 {
		return nil
	}
	frames, err := d.buildFrames(data, pidTypes)
	if err != nil {
		return fmt.Errorf("frame creation error: %w", err)
	}
	if frames == nil {
		return nil
	}
	for i, fr := range frames {
		select {
		case frameChan <- fr:
		case <-d.ctx.Done():
			fr.Decommission()
		default:
			for _, remaining := range frames[i+1:] {
				remaining.Decommission()
			}
			fr.Decommission()
			return fmt.Errorf("frame channel send error: %w", d.ctx.Err())
		}
	}
	return nil
}

func (d *MpegTsDemuxer) buildFrames(data *astits.DemuxerData, pidTypes map[uint16]astits.StreamType) ([]types.Frame, error) {
	pts, dts, err := d.getTimestamps(data.PES)
	if err != nil {
		return nil, fmt.Errorf("timestamp extraction error: %w", err)
	}
	streamType, ok := pidTypes[data.PID]
	if !ok {
		return nil, nil
	}
	codecID := mapStreamType(streamType)
	if codecID == codec.CODECID_UNRECOGNIZED {
		return nil, fmt.Errorf("unknown stream type: %s", streamType)
	}
	payload := make([]byte, len(data.PES.Data))
	copy(payload, data.PES.Data)
	data.PES.Data = nil // allow GC to reclaim original slice memory

	if codecID == codec.CODECID_AUDIO_AAC {
		return d.aacFrames.SplitFrameWithTiming(payload, pts, dts), nil
	}
	frame, err := buffer.NewFrameFromData(
		types.FrameHeader{
			Cid: codecID,
			Pts: pts,
			Dts: dts,
		},
		payload,
	)
	if err != nil {
		return nil, fmt.Errorf("frame creation error: %w", err)
	}
	return []types.Frame{frame}, nil
}

func (d *MpegTsDemuxer) getTimestamps(pes *astits.PESData) (time.Duration, time.Duration, error) {
	if pes.Header.OptionalHeader == nil {
		return 0, 0, errors.New("missing optional header")
	}
	oh := pes.Header.OptionalHeader
	if oh.PTS == nil {
		return 0, 0, errors.New("missing PTS")
	}
	rawPts := oh.PTS.Base
	unwrappedPts := unwrapTs(rawPts, d.prevPts, d.ptsSet)
	d.prevPts = unwrappedPts
	d.ptsSet = true
	pts := time.Duration(unwrappedPts * 100_000 / 9)
	var rawDts int64
	var dts time.Duration
	if oh.DTS != nil {
		rawDts = oh.DTS.Base
		unwrappedDts := unwrapTs(rawDts, d.prevDts, d.dtsSet)
		d.prevDts = unwrappedDts
		d.dtsSet = true
		dts = time.Duration(unwrappedDts * 100_000 / 9)
	} else {
		dts = pts
		d.prevDts = unwrappedPts
		d.dtsSet = d.ptsSet
	}
	return pts, dts, nil
}

func (d *MpegTsDemuxer) Close() {
	d.cancel()
	if d.buffer != nil {
		d.buffer.Close()
	}
	d.wg.Wait()
	d.astDemux = nil
	d.buffer = nil
}
