package hls

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

type Connection struct {
	logger zerolog.Logger
	// sampledLogger is for problems that can repeat on every frame
	sampledLogger zerolog.Logger

	location types.Location

	seg       *segmenter
	stream    *liveStream
	createdAt time.Time

	wg     sync.WaitGroup
	mtx    sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
}

func NewConnection(upstreamCtx context.Context, logger zerolog.Logger, location types.Location, stream *liveStream) *Connection {
	ctx, cancel := context.WithCancel(upstreamCtx)
	return &Connection{
		logger:        logger,
		sampledLogger: logger.Sample(&zerolog.BurstSampler{Burst: 5, Period: 10 * time.Second}),
		location:      location,
		stream:        stream,
		createdAt:     time.Now(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (c *Connection) GetStream() *gortsplib.ServerStream {
	return nil
}

// HandleHTTP serves the stream this connection feeds.
func (c *Connection) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	c.stream.handleHTTP(w, r, c)
}

func (c *Connection) muxerPrimed() bool {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	return c.seg != nil
}

func (c *Connection) StartPlay() error {
	return nil
}

func (c *Connection) GetAddr() net.Addr {
	// Dummy address since this multiplexer serves many HTTP clients.
	return &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 0}
}

func (c *Connection) GetType() types.ConnectionType {
	return types.ConnectionTypeReader
}

func (c *Connection) GetCtx() context.Context {
	return c.ctx
}

func (c *Connection) GetLocation() types.Location {
	return c.location
}

func (c *Connection) GetLogger() zerolog.Logger {
	return c.logger
}

func (c *Connection) Write(streams []types.BufferOutput) error {
	var audioCh, videoCh chan types.Frame
	for _, stream := range streams {
		switch stream.Type {
		case types.BufferTypeVideo:
			videoCh = stream.Channel
		case types.BufferTypeAudio:
			audioCh = stream.Channel
		}
	}
	if audioCh == nil || videoCh == nil {
		return errors.New("missing audio or video stream from haven")
	}

	sps, pps, asc, err := c.extractMetadata(&videoCh, &audioCh)
	if err != nil {
		return fmt.Errorf("failed to extract streams metadata: %w", err)
	}
	c.logger.Info().
		Int("sps_bytes", len(sps)).
		Int("pps_bytes", len(pps)).
		Int("audio_sample_rate", asc.SampleRate).
		Uint8("audio_channel_config", asc.ChannelConfig).
		Dur("since_connection_created", time.Since(c.createdAt)).
		Msg("Extracted stream metadata, priming HLS muxer")

	err = c.primeMuxer(sps, pps)
	if err != nil {
		return fmt.Errorf("failed to prime HLS muxer: %w", err)
	}
	c.wg.Go(c.monitor)

	c.handleFrames(videoCh, c.writeH264)
	c.handleFrames(audioCh, c.writeAAC)

	return nil
}

func (c *Connection) Close() {
	c.cancel()
	c.wg.Wait()

	c.logger.Info().Msg("HLS connection closed")
}

//
// Helper functions
//

// extractMetadata reads from the provided video and audio channels and writes them to new channels in place.
// It extracts the SPS/PPS from the video stream and the Audio Specific Config from the audio stream, which are needed to prime the HLS muxer.
func (c *Connection) extractMetadata(videoCh, audioCh *chan types.Frame) (sps, pps []byte, asc *mpeg4audio.AudioSpecificConfig, err error) {
	upstreamVideoCh := *videoCh
	upstreamAudioCh := *audioCh
	outVideoCh := make(chan types.Frame, cap(upstreamVideoCh))
	outAudioCh := make(chan types.Frame, cap(upstreamAudioCh))
	*videoCh = outVideoCh
	*audioCh = outAudioCh

	type metadata struct {
		sps, pps []byte
		asc      *mpeg4audio.AudioSpecificConfig
	}
	found := make(chan metadata, 1)

	c.wg.Go(func() {
		defer close(found)
		defer close(outVideoCh)
		defer close(outAudioCh)

		forward := func(ch chan types.Frame, frame types.Frame) bool {
			select {
			case ch <- frame:
				return true
			case <-c.ctx.Done():
				frame.Decommission()
				return false
			}
		}

		var meta metadata
		reported := false
		for {
			select {
			case frame, ok := <-upstreamVideoCh:
				if !ok {
					return
				}
				if meta.sps == nil || meta.pps == nil {
					if extractedSps, extractedPps, err := ExtractSPSPPS(frame); err == nil {
						meta.sps, meta.pps = extractedSps, extractedPps
					}
				}
				if !forward(outVideoCh, frame) {
					return
				}

			case frame, ok := <-upstreamAudioCh:
				if !ok {
					return
				}
				if meta.asc == nil {
					if extractedAsc, err := ExtractASC(frame); err == nil {
						meta.asc = extractedAsc
					}
				}
				if !forward(outAudioCh, frame) {
					return
				}

			case <-c.ctx.Done():
				return
			}

			if !reported && meta.sps != nil && meta.pps != nil && meta.asc != nil {
				found <- meta
				reported = true
			}
		}
	})

	select {
	case meta, ok := <-found:
		if !ok {
			return nil, nil, nil, errors.New("stream ended while waiting for metadata")
		}
		return meta.sps, meta.pps, meta.asc, nil
	case <-c.ctx.Done():
		return nil, nil, nil, errors.New("context cancelled while waiting for metadata")
	}
}

func (c *Connection) primeMuxer(sps, pps []byte) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	mux, err := newTSMuxer(c.ctx, sps, pps)
	if err != nil {
		return fmt.Errorf("failed to create HLS TS muxer: %w", err)
	}

	c.seg = newSegmenter(c.logger, mux, c.stream)
	c.stream.beginFeed()

	return nil
}

func (c *Connection) handleFrames(packetCh <-chan types.Frame, muxFunc func(frame types.Frame) error) {
	c.wg.Go(func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case frame, ok := <-packetCh:
				if !ok {
					c.cancel()
					return
				}

				if err := muxFunc(frame); err != nil {
					c.sampledLogger.Error().Err(err).Msg("Failed to mux frame")
				}
				frame.Decommission()
			}
		}
	})
}

func (c *Connection) writeH264(frame types.Frame) error {
	frameData := frame.Data()
	var nalus [][]byte

	codec.SplitFrame(frameData, func(nalu []byte) bool {
		if len(nalu) == 0 {
			return true
		}
		naluCopy := make([]byte, len(nalu))
		copy(naluCopy, nalu)
		nalus = append(nalus, naluCopy)
		return true
	})

	if len(nalus) == 0 {
		return nil
	}

	baseNanos := frame.Header().Pts.Nanoseconds()
	if baseNanos < 0 {
		return fmt.Errorf("invalid PTS for H264 frame: %d", baseNanos)
	}
	dtsNanos := frame.Header().Dts.Nanoseconds()
	if dtsNanos < 0 {
		return fmt.Errorf("invalid DTS for H264 frame: %d", dtsNanos)
	}

	ptsTicks := (baseNanos * 90000) / 1000000000
	dtsTicks := (dtsNanos * 90000) / 1000000000

	c.mtx.Lock()
	defer c.mtx.Unlock()

	return c.seg.onVideoFrame(nalus, frame, ptsTicks, dtsTicks)
}

// writeAAC keeps each ADTS frame's header intact.
func (c *Connection) writeAAC(frame types.Frame) error {
	frameData := frame.Data()
	basePts := frame.Header().Pts

	var adts []byte
	codec.SplitAACFrame(frameData, func(aac []byte) {
		adts = append(adts, aac...)
	})
	if len(adts) == 0 {
		return errors.New("no AAC frames found in audio frame")
	}

	baseNanos := basePts.Nanoseconds()
	if baseNanos < 0 {
		return fmt.Errorf("invalid PTS for AAC frame: %d", baseNanos)
	}

	// PES timestamps always run on the fixed 90kHz MPEG-TS clock, regardless of the audio sample rate.
	ptsTicks := (baseNanos * 90000) / 1000000000

	c.mtx.Lock()
	defer c.mtx.Unlock()

	return c.seg.onAudioFrame(adts, frame, ptsTicks)
}
