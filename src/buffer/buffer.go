package buffer

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Stream-Haven/src/types"
)

type muxBuffer struct {
	logger zerolog.Logger

	videoBuf types.SubBuffer
	audioBuf types.SubBuffer
}

func NewBuffer(log zerolog.Logger) types.Buffer {
	b := &muxBuffer{
		logger:   log,
		videoBuf: newBufferStream(log.With().Str("track", "video").Logger(), types.BufferTypeVideo),
		audioBuf: newBufferStream(log.With().Str("track", "audio").Logger(), types.BufferTypeAudio),
	}
	return b
}

func (b *muxBuffer) IsReady() bool {
	return b.videoBuf.IsReady() && b.audioBuf.IsReady()
}

// Write writes a frame to the appropriate buffer based on its codec and also to the interleaved buffer.
func (b *muxBuffer) Write(frame types.Frame) error {
	defer frame.Decommission()

	switch frame.Header().Cid {
	case codec.CODECID_VIDEO_H264:
		if err := b.videoBuf.Write(frame.Clone()); err != nil {
			return err
		}
	case codec.CODECID_AUDIO_AAC:
		if err := b.audioBuf.Write(frame.Clone()); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported codec: %s", codec.CodecString(frame.Header().Cid))
	}

	return nil
}

func (b *muxBuffer) Subscribe(ctx context.Context, offset time.Duration) ([]types.BufferOutput, error) {
	video, err := b.videoBuf.SubscribeOffset(ctx, offset)
	if err != nil {
		return nil, fmt.Errorf("video subscribe failed: %w", err)
	}

	audio, err := b.audioBuf.SubscribePTS(ctx, video[0].StartPTS)
	if err != nil {
		return nil, fmt.Errorf("audio subscribe failed: %w", err)
	}

	return append(audio, video...), nil
}

func (b *muxBuffer) Close() {
	b.videoBuf.Cancel()
	b.audioBuf.Cancel()

	b.videoBuf.Close()
	b.audioBuf.Close()
}
