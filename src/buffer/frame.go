package buffer

import (
	"bytes"
	"errors"

	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

var ErrEmptyPayload = errors.New("payload is empty")

// muxFrame owns an exact-size copy of its payload.
// Frames are not pooled: a shared pool hands buffers grown by IDR frames to small P-frames and audio frames,
// so every frame held in the ring buffers slowly ratchets up to the size of the largest IDR.
type muxFrame struct {
	hdr     types.FrameHeader
	payload []byte
}

func NewFrameFromData(header types.FrameHeader, data []byte) (types.Frame, error) {
	if len(data) == 0 {
		return nil, ErrEmptyPayload
	}
	return &muxFrame{
		hdr:     header,
		payload: bytes.Clone(data),
	}, nil
}

func (f *muxFrame) Decommission() {
	f.payload = nil
}

func (f *muxFrame) Clone() types.Frame {
	return &muxFrame{
		hdr:     f.hdr,
		payload: bytes.Clone(f.payload),
	}
}

func (f *muxFrame) Header() *types.FrameHeader {
	return &f.hdr
}

func (f *muxFrame) SetData(data []byte) {
	f.payload = bytes.Clone(data)
}

func (f *muxFrame) Data() []byte {
	return f.payload
}

func (f *muxFrame) Len() uint64 {
	return uint64(len(f.payload))
}

func (f *muxFrame) IsKeyFrame() bool {
	isKF := false
	codec.SplitFrameWithStartCode(f.Data(), func(nalu []byte) bool {
		if codec.H264NaluType(nalu) == codec.H264_NAL_I_SLICE && codec.IsH264IDRFrame(nalu) {
			isKF = true
			return false
		}
		return true
	})
	return isKF
}
