package hls

import (
	"bytes"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Stream-Haven/src/types"
)

const (
	// Segments are cut at the first keyframe after segmentMinDuration, so an encoder
	// keyframe interval of 1s yields 1s segments (lowest latency), longer GOPs yield
	// GOP-length segments.
	segmentMinDuration = 1 * time.Second
	// Keyframe timestamps jitter slightly; without slack a 0.99s GOP would be skipped
	// and every other segment would end up 2s long.
	segmentCutTolerance = 100 * time.Millisecond
	// longSegmentWarning is the segment duration above which latency suffers noticeably.
	longSegmentWarning = 3 * time.Second
)

type segmenter struct {
	logger   zerolog.Logger
	mux      *tsMuxer
	playlist *livePlaylist

	nextSeq uint64

	current      *bytes.Buffer
	currentSeq   uint64
	segmentStart time.Duration
	started      bool

	warnedLongSegment bool
}

func newSegmenter(logger zerolog.Logger, mux *tsMuxer, playlist *livePlaylist) *segmenter {
	return &segmenter{
		logger:   logger,
		mux:      mux,
		playlist: playlist,
		// Seeding from the clock keeps EXT-X-MEDIA-SEQUENCE increasing across publisher
		// reconnects (players break when it goes backwards) and keeps segment names unique,
		// so no cache can serve a segment from a previous session. Unix seconds stay below
		// 2^31 for players that parse the sequence as int32.
		nextSeq: uint64(time.Now().Unix()), // #nosec G115 -- always positive
	}
}

func segmentFilename(seq uint64) string {
	return fmt.Sprintf("segment_%d.ts", seq)
}

func parseSegmentSeq(filename string) (uint64, bool) {
	var seq uint64
	if n, err := fmt.Sscanf(filename, "segment_%d.ts", &seq); err != nil || n != 1 {
		return 0, false
	}
	return seq, true
}

func (s *segmenter) ready() bool {
	return s.playlist.ready()
}

func (s *segmenter) playlistSnapshot() []byte {
	return s.playlist.snapshot()
}

func (s *segmenter) segmentByFilename(filename string) ([]byte, bool) {
	seq, ok := parseSegmentSeq(filename)
	if !ok {
		return nil, false
	}
	return s.playlist.segment(seq)
}

func (s *segmenter) startSegment(pts time.Duration) {
	s.current = &bytes.Buffer{}
	s.mux.setOutput(s.current)
	s.currentSeq = s.nextSeq
	s.nextSeq++
	s.segmentStart = pts
	s.started = true
}

func (s *segmenter) finishSegment(endPTS time.Duration) {
	duration := endPTS - s.segmentStart
	if duration > longSegmentWarning && !s.warnedLongSegment {
		s.warnedLongSegment = true
		s.logger.Warn().Dur("segment_duration", duration).
			Msg("Encoder keyframe interval is long, HLS latency will be high. Set the keyframe interval to 1s for lowest latency")
	}

	s.playlist.addSegment(segment{
		seq:      s.currentSeq,
		duration: duration,
		data:     s.current.Bytes(),
	})
}

func (s *segmenter) onVideoFrame(nalus [][]byte, frame types.Frame, ptsTicks, dtsTicks int64) error {
	pts := frame.Header().Pts
	keyFrame := frame.IsKeyFrame()

	if !s.started {
		// Every segment must start with an IDR, many players cannot start decoding otherwise
		if !keyFrame {
			return nil
		}
		s.startSegment(pts)
	} else if keyFrame && pts-s.segmentStart >= segmentMinDuration-segmentCutTolerance {
		s.finishSegment(pts)
		s.startSegment(pts)
	}

	return s.mux.writeVideoAccessUnit(nalus, ptsTicks, dtsTicks, keyFrame)
}

func (s *segmenter) onAudioFrame(adts []byte, ptsTicks int64) error {
	if !s.started {
		// Audio arrived before the first video keyframe opened a segment drop it rather than buffer indefinitely
		return nil
	}
	return s.mux.writeAudioAccessUnit(adts, ptsTicks)
}
