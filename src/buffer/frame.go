package buffer

import (
	"bytes"
	"errors"
	"sync"

	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

const (
	// MaxPayloadSize is the initial pool buffer capacity. 64KB covers essentially all
	// P/B frames and audio at normal bitrates; IDR frames that exceed it are still
	// returned to the pool so they can be reused without re-growing on the next IDR.
	MaxPayloadSize = 64 * 1024
)

var ErrEmptyPayload = errors.New("payload is empty")

type muxFrame struct {
	hdr     types.FrameHeader
	payload *bytes.Buffer
}

type bufPool struct {
	pool sync.Pool
}

func newPool() *bufPool {
	return &bufPool{
		pool: sync.Pool{
			New: func() any {
				return bytes.NewBuffer(make([]byte, 0, MaxPayloadSize))
			},
		},
	}
}

func (p *bufPool) Get() *bytes.Buffer {
	b, ok := p.pool.Get().(*bytes.Buffer)
	if !ok {
		return bytes.NewBuffer(make([]byte, 0, MaxPayloadSize))
	}
	return b
}

func (p *bufPool) Put(b *bytes.Buffer) {
	b.Reset()
	p.pool.Put(b)
}

var payloadPool *bufPool = newPool()

func NewFrameFromData(header types.FrameHeader, data []byte) (types.Frame, error) {
	if len(data) == 0 {
		return nil, ErrEmptyPayload
	}
	f := &muxFrame{
		hdr:     header,
		payload: payloadPool.Get(),
	}
	f.payload.Write(data)
	return f, nil
}

func (f *muxFrame) Decommission() {
	if f.payload == nil {
		return
	}
	payloadPool.Put(f.payload)
	f.payload = nil
}

func (f *muxFrame) Clone() types.Frame {
	clone := &muxFrame{}
	clone.hdr = f.hdr
	clone.payload = payloadPool.Get()
	if f.payload != nil {
		clone.payload.Write(f.payload.Bytes())
	}
	return clone
}

func (f *muxFrame) Header() *types.FrameHeader {
	return &f.hdr
}

func (f *muxFrame) SetData(data []byte) {
	f.payload.Reset()
	f.payload.Write(data)
}

func (f *muxFrame) Data() []byte {
	return f.payload.Bytes()
}

func (f *muxFrame) Len() uint64 {
	return uint64(f.payload.Len()) //nolint:gosec // Buffer.Len() is always non-negative
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
