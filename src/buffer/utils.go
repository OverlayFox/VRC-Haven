package buffer

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Stream-Haven/src/types"
)

// newDropLogger rate limits dropped frame warnings, which otherwise repeat for every frame while a reader lags.
func newDropLogger(logger zerolog.Logger) zerolog.Logger {
	return logger.Sample(&zerolog.BurstSampler{Burst: 1, Period: 5 * time.Second})
}

func CloseAndDrain(ch chan types.Frame) {
	close(ch)
	for frame := range ch {
		if frame != nil {
			frame.Decommission()
		}
	}
}

func DetectH264Format(data []byte) (types.FrameFormat, error) {
	if len(data) == 0 {
		return types.FrameFormatUnknown, errors.New("empty data")
	}
	start, _ := codec.FindStartCode(data, 0)
	if start != -1 {
		return types.FrameFormatAnnexB, nil
	}
	if len(data) >= 4 {
		nalLen := binary.BigEndian.Uint32(data[:4])
		if nalLen > 0 && nalLen < 10*1024*1024 && int(nalLen)+4 <= len(data) {
			return types.FrameFormatAVCC, nil
		}
	}
	return types.FrameFormatUnknown, errors.New("cannot detect H.264 format")
}
