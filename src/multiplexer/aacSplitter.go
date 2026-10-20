package multiplexer

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Stream-Haven/src/buffer"
	"github.com/OverlayFox/VRC-Stream-Haven/src/types"
)

const (
	SamplesPerFrame = 1024
)

type AACFrameSplitter struct {
	logger zerolog.Logger

	recentPTS     time.Duration
	recentDTS     time.Duration
	maxAudioDrift time.Duration

	isInitialized atomic.Bool
}

func NewAACFrameSplitter(logger zerolog.Logger, maxAudioDrift time.Duration) *AACFrameSplitter {
	return &AACFrameSplitter{
		logger:        logger,
		maxAudioDrift: maxAudioDrift,
	}
}

func (s *AACFrameSplitter) calculateFrameDuration(frameData []byte) (time.Duration, error) {
	if len(frameData) < 7 {
		return 0, errors.New("adts frame is too short")
	}
	var adtsHeader codec.ADTS_Frame_Header
	adtsHeader.Decode(frameData)
	if adtsHeader.Fix_Header.Sampling_frequency_index >= uint8(len(codec.AAC_Sampling_Idx)) {
		return 0, fmt.Errorf("invalid sampling frequency index: %d", adtsHeader.Fix_Header.Sampling_frequency_index)
	}
	rate := codec.AAC_Sampling_Idx[adtsHeader.Fix_Header.Sampling_frequency_index]
	if rate == 0 {
		return 0, errors.New("invalid sample rate: 0")
	}
	return time.Duration(SamplesPerFrame) * time.Second / time.Duration(rate), nil
}

func (s *AACFrameSplitter) SplitFrameWithTiming(pesData []byte, basePTS, baseDTS time.Duration) []types.Frame {
	if s.isInitialized.Load() {
		drift := (basePTS - s.recentPTS).Abs()
		if drift > s.maxAudioDrift {
			s.logger.Warn().Dur("drift_ms", drift).
				Dur("recent_pts_ms", s.recentPTS).
				Dur("new_base_pts_ms", basePTS).
				Msg("PTS drift detected, resynchronizing AAC frame splitter timestamps")
			s.recentPTS = basePTS
			s.recentDTS = baseDTS
		}
	} else {
		s.recentPTS = basePTS
		s.recentDTS = baseDTS
		s.isInitialized.Store(true)
		s.logger.Debug().Dur("pts", basePTS).
			Dur("dts", baseDTS).
			Msg("Initialized AAC frame splitter timestamps")
	}

	var frames []types.Frame
	recentPTS := s.recentPTS
	recentDTS := s.recentDTS

	codec.SplitAACFrame(pesData, func(aacFrameData []byte) {
		duration, err := s.calculateFrameDuration(aacFrameData)
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to calculate AAC frame duration")
			return // invalid header
		}

		frame, err := buffer.NewFrameFromData(types.FrameHeader{
			Cid: codec.CODECID_AUDIO_AAC,
			Pts: recentPTS,
			Dts: recentDTS,
		}, aacFrameData)
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to create frame from AAC data")
			return // invalid frame
		}

		frames = append(frames, frame)

		recentPTS += duration
		recentDTS += duration
	})

	if len(frames) > 0 {
		s.recentPTS = recentPTS
		s.recentDTS = recentDTS
	}

	return frames
}
