package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

const playlistWaitTimeout = 7 * time.Second

type Connection struct {
	logger zerolog.Logger
	// sampledLogger is for problems that can repeat on every frame
	sampledLogger zerolog.Logger

	location types.Location

	seg       *segmenter
	ready     chan struct{}
	readyOnce sync.Once
	createdAt time.Time

	viewers *viewerTracker

	wg     sync.WaitGroup
	mtx    sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
}

func NewConnection(upstreamCtx context.Context, logger zerolog.Logger, location types.Location) *Connection {
	ctx, cancel := context.WithCancel(upstreamCtx)
	return &Connection{
		logger:        logger,
		sampledLogger: logger.Sample(&zerolog.BurstSampler{Burst: 5, Period: 10 * time.Second}),
		location:      location,
		ready:         make(chan struct{}),
		createdAt:     time.Now(),
		viewers:       newViewerTracker(logger),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (c *Connection) GetStream() *gortsplib.ServerStream {
	return nil
}

// HandleHTTP serves the live playlist and its in-RAM MPEG-TS segments to HTTP clients.
// Responses are written without holding the connection lock, so slow or distant viewers never stall muxing.
func (c *Connection) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	filename := path.Base(r.URL.Path)

	if filename == playlistFilename {
		c.servePlaylist(w, r)
		return
	}

	c.mtx.RLock()
	var data []byte
	var info playlistInfo
	var seq uint64
	ok, hasInfo, isSegment := false, false, false
	if c.seg != nil {
		data, ok = c.seg.segmentByFilename(filename)
		info, hasInfo = c.seg.playlistInfo()
		seq, isSegment = c.seg.parseSegmentFilename(filename)
	}
	c.mtx.RUnlock()

	if !ok {
		if notFound, shouldLog := c.viewers.segmentNotFound(r); shouldLog {
			c.logSegmentNotFound(r, filename, notFound, seq, isSegment, info, hasInfo)
		}
		http.NotFound(w, r)
		return
	}
	c.viewers.segmentServed(r, seq)

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "public, max-age=60")
	serveBytes(w, r, data)
}

// servePlaylist holds the request until the stream is primed instead of failing it, since many players give up on the first error.
func (c *Connection) servePlaylist(w http.ResponseWriter, r *http.Request) {
	timer := time.NewTimer(playlistWaitTimeout)
	defer timer.Stop()

	select {
	case <-c.ready:
	case <-r.Context().Done():
		logClient(c.logger.Info(), r).Msg("Client gave up on playlist request while the stream was priming")
		return
	case <-c.ctx.Done():
		logClient(c.logger.Info(), r).Msg("Playlist requested after the stream ended")
		http.Error(w, "Stream ended", http.StatusServiceUnavailable)
		return
	case <-timer.C:
		c.mtx.RLock()
		muxerPrimed := c.seg != nil
		c.mtx.RUnlock()
		logClient(c.logger.Warn(), r).
			Bool("muxer_primed", muxerPrimed).
			Dur("since_connection_created", time.Since(c.createdAt)).
			Msg("Playlist request timed out waiting for the stream to prime")
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Stream not primed yet", http.StatusServiceUnavailable)
		return
	}

	c.mtx.RLock()
	data := c.seg.playlistSnapshot()
	info, _ := c.seg.playlistInfo()
	c.mtx.RUnlock()

	c.viewers.playlistServed(r, info)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	serveBytes(w, r, data)
}

func (c *Connection) logSegmentNotFound(r *http.Request, filename string, notFound int, seq uint64, isSegment bool, info playlistInfo, hasInfo bool) {
	event := logClient(c.logger.Warn(), r).Str("file", filename).Int("segments_not_found", notFound)
	switch {
	case !isSegment:
		event.Msg("Unknown HLS file requested") // Usually a segment of an earlier stream session from a cached playlist
		return
	case !hasInfo:
		event.Msg("HLS segment requested before any segment exists")
		return
	}

	event = event.Uint64("oldest_seq", info.oldestSeq).Uint64("newest_seq", info.newestSeq)
	switch {
	case seq < info.oldestSeq:
		event.Uint64("segments_behind", info.oldestSeq-seq).Msg("HLS segment already expired, the player fell too far behind live")
	default:
		event.Uint64("segments_ahead", seq-info.newestSeq).Msg("HLS segment does not exist yet")
	}
}

// serveBytes answers with an explicit Content-Length (no chunked encoding) and handles HEAD and Range requests, which some players issue even for live streams.
func serveBytes(w http.ResponseWriter, r *http.Request, data []byte) {
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Header.Get("Range") != "" {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data) // #nosec G705 -- our own playlist/MPEG-TS bytes, not user input
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
//
//nolint:gocognit // This function is a bit complex due to the need to read from both channels concurrently and wait until both metadata are extracted before returning.
func (c *Connection) extractMetadata(videoCh, audioCh *chan types.Frame) (sps, pps []byte, asc *mpeg4audio.AudioSpecificConfig, err error) {
	upstreamVideoCh := *videoCh
	upstreamAudioCh := *audioCh
	*videoCh = make(chan types.Frame, cap(upstreamVideoCh))
	*audioCh = make(chan types.Frame, cap(upstreamAudioCh))

	receiveDone := make(chan struct{}, 2)

	c.wg.Go(func() {
		defer close(receiveDone)
		defer close(*videoCh)
		defer close(*audioCh)

		for {
			select {
			case frame, ok := <-upstreamVideoCh:
				if !ok {
					return
				}
				if sps != nil && pps != nil {
					*videoCh <- frame
					continue
				}

				if sps == nil || pps == nil {
					extractedSps, extractedPps, err := ExtractSPSPPS(frame)
					if err == nil {
						sps, pps = extractedSps, extractedPps
						if sps != nil && pps != nil && asc != nil {
							receiveDone <- struct{}{}
						}
					}
				}
				*videoCh <- frame

			case frame, ok := <-upstreamAudioCh:
				if !ok {
					return
				}
				if asc != nil {
					*audioCh <- frame
					continue
				}

				if asc == nil {
					extractedAsc, err := ExtractASC(frame)
					if err == nil {
						asc = extractedAsc
						if sps != nil && pps != nil && asc != nil {
							receiveDone <- struct{}{}
						}
					}
				}
				*audioCh <- frame

			case <-c.ctx.Done():
				return
			}
		}
	})

	select {
	case <-receiveDone:
		return sps, pps, asc, nil
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

	c.seg = newSegmenter(c.logger, mux, newLivePlaylist())

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

	if err := c.seg.onVideoFrame(nalus, frame, ptsTicks, dtsTicks); err != nil {
		return err
	}
	if c.seg.ready() {
		c.readyOnce.Do(func() {
			c.logger.Info().Dur("since_connection_created", time.Since(c.createdAt)).Msg("HLS stream primed, serving playlist")
			close(c.ready)
		})
	}
	return nil
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

	// PES timestamps always run on the fixed 90kHz MPEG-TS clock, regardless
	// of the audio sample rate.
	ptsTicks := (baseNanos * 90000) / 1000000000

	c.mtx.Lock()
	defer c.mtx.Unlock()

	return c.seg.onAudioFrame(adts, frame, ptsTicks)
}
