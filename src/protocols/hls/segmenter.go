package hls

import (
	"bytes"
	"time"

	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

const (
	segmentMinDuration   = 1 * time.Second
	segmentCutTolerance  = 100 * time.Millisecond
	longSegmentWarning   = 3 * time.Second
	timestampJumpWarning = 1 * time.Second
	avOffsetWarning      = 500 * time.Millisecond
)

// streamHealth records when media last flowed into the segmenter, so a stalled playlist can be traced back to the publisher, missing keyframes or the muxer.
type streamHealth struct {
	lastVideoAt    time.Time
	lastAudioAt    time.Time
	lastKeyFrameAt time.Time
	lastSegmentAt  time.Time
	lastVideoPTS   time.Duration
	videoFrames    uint64
	audioFrames    uint64
	segments       uint64
}

// segmentStats describes the segment currently being written.
type segmentStats struct {
	videoFrames   int
	audioFrames   int
	firstVideoPTS time.Duration
	firstAudioPTS time.Duration
}

// timestampTracker warns about timestamps that go backwards or jump, both of which make players wait for media that never arrives.
type timestampTracker struct {
	track string
	last  time.Duration
	seen  bool
}

func (t *timestampTracker) check(logger zerolog.Logger, ts time.Duration) {
	if t.seen {
		switch delta := ts - t.last; {
		case delta <= 0:
			logger.Warn().Str("track", t.track).Dur("previous", t.last).Dur("current", ts).Msg("Timestamps are not increasing")
		case delta > timestampJumpWarning:
			logger.Warn().Str("track", t.track).Dur("gap", delta).Dur("current", ts).Msg("Timestamps jumped forward")
		}
	}
	t.last = ts
	t.seen = true
}

type segmenter struct {
	logger zerolog.Logger
	// sampledLogger is for problems that can repeat on every frame
	sampledLogger zerolog.Logger
	mux           *tsMuxer
	stream        *liveStream

	current      *bytes.Buffer
	segmentStart time.Duration
	started      bool

	warnedLongSegment bool

	stats        segmentStats
	health       streamHealth
	videoDTS     timestampTracker
	audioPTS     timestampTracker
	droppedVideo int
	droppedAudio int
}

func newSegmenter(logger zerolog.Logger, mux *tsMuxer, stream *liveStream) *segmenter {
	return &segmenter{
		logger:        logger,
		sampledLogger: logger.Sample(&zerolog.BurstSampler{Burst: 5, Period: 10 * time.Second}),
		mux:           mux,
		stream:        stream,
		videoDTS:      timestampTracker{track: "video"},
		audioPTS:      timestampTracker{track: "audio"},
	}
}

func (s *segmenter) streamHealth() streamHealth {
	return s.health
}

func (s *segmenter) startSegment(pts time.Duration) {
	s.current = &bytes.Buffer{}
	s.mux.setOutput(s.current)
	s.segmentStart = pts
	s.started = true
	s.stats = segmentStats{}
}

func (s *segmenter) finishSegment(endPTS time.Duration) {
	duration := endPTS - s.segmentStart
	if duration > longSegmentWarning && !s.warnedLongSegment {
		s.warnedLongSegment = true
		s.logger.Warn().Dur("segment_duration", duration).
			Msg("Encoder keyframe interval is long, HLS latency will be high. Set the keyframe interval to 1s for lowest latency")
	}

	seq := s.stream.addSegment(duration, s.current.Bytes())
	s.health.lastSegmentAt = time.Now()
	s.health.segments++

	if s.stats.audioFrames == 0 {
		s.sampledLogger.Warn().Uint64("seq", seq).Msg("Segment has no audio, players may stall waiting for it")
	} else {
		avOffset := s.stats.firstAudioPTS - s.stats.firstVideoPTS
		if avOffset.Abs() > avOffsetWarning {
			s.sampledLogger.Warn().Uint64("seq", seq).Dur("av_offset", avOffset).
				Msg("Audio and video timestamps are far apart, players may stall waiting for them to line up")
		}
	}
}

func (s *segmenter) onVideoFrame(nalus [][]byte, frame types.Frame, ptsTicks, dtsTicks int64) error {
	pts := frame.Header().Pts
	dts := frame.Header().Dts
	keyFrame := frame.IsKeyFrame()

	if pts < dts {
		s.sampledLogger.Warn().Dur("pts", pts).Dur("dts", dts).Msg("Video frame has PTS before DTS")
	}
	s.videoDTS.check(s.sampledLogger, dts)
	now := time.Now()
	s.health.lastVideoAt = now
	s.health.lastVideoPTS = pts
	s.health.videoFrames++
	if keyFrame {
		s.health.lastKeyFrameAt = now
	}

	if !s.started {
		// Every segment must start with an IDR, many players cannot start decoding otherwise
		if !keyFrame {
			s.droppedVideo++
			return nil
		}
		s.logger.Info().
			Int("dropped_video_frames", s.droppedVideo).
			Int("dropped_audio_frames", s.droppedAudio).
			Dur("first_pts", pts).
			Msg("First keyframe received, HLS segmenting started")
		s.startSegment(pts)
	} else if keyFrame && pts-s.segmentStart >= segmentMinDuration-segmentCutTolerance {
		s.finishSegment(pts)
		s.startSegment(pts)
	}

	if s.stats.videoFrames == 0 {
		s.stats.firstVideoPTS = pts
	}
	s.stats.videoFrames++

	return s.mux.writeVideoAccessUnit(nalus, ptsTicks, dtsTicks, keyFrame)
}

func (s *segmenter) onAudioFrame(adts []byte, frame types.Frame, ptsTicks int64) error {
	pts := frame.Header().Pts
	s.audioPTS.check(s.sampledLogger, pts)
	s.health.lastAudioAt = time.Now()
	s.health.audioFrames++

	if !s.started {
		// Audio arrived before the first video keyframe opened a segment drop it rather than buffer indefinitely
		s.droppedAudio++
		return nil
	}

	if s.stats.audioFrames == 0 {
		s.stats.firstAudioPTS = pts
	}
	s.stats.audioFrames++

	return s.mux.writeAudioAccessUnit(adts, ptsTicks)
}
