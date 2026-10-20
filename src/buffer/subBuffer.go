package buffer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Stream-Haven/src/types"
)

const (
	MinKeyFrames = 3
	WaitForIDR   = 8 * time.Second
	BufCap       = 500
)

type subBuffer struct {
	logger zerolog.Logger

	circBuf *circularBuffer
	cap     int

	keyFrames *types.OrderedMap[time.Duration, int]
	curPTS    time.Duration
	startPTS  time.Duration
	started   bool

	format  types.FrameFormat
	bufType types.BufferType

	idrOnce    sync.Once
	idrExpired atomic.Bool

	mtx    sync.RWMutex
	ready  atomic.Bool
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func newBufferStream(logger zerolog.Logger, bufType types.BufferType) types.SubBuffer {
	ctx, cancel := context.WithCancel(context.Background())
	return &subBuffer{
		logger:    logger,
		circBuf:   newCircularBuffer(logger, BufCap),
		cap:       BufCap,
		keyFrames: types.NewOrderedMap[time.Duration, int](),
		format:    types.FrameFormatUnknown,
		bufType:   bufType,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (b *subBuffer) IsReady() bool {
	return b.ready.Load()
}

func (b *subBuffer) Write(frame types.Frame) error {
	switch b.bufType {
	case types.BufferTypeVideo:
		return b.writeVideo(frame)
	case types.BufferTypeAudio:
		return b.writeAudio(frame)
	default:
		frame.Decommission()
		return fmt.Errorf("unsupported buffer type: %s", b.bufType)
	}
}

func (b *subBuffer) writeVideo(frame types.Frame) error {
	if frame.Header().Cid != codec.CODECID_VIDEO_H264 {
		frame.Decommission()
		return fmt.Errorf("invalid video codec: %s", codec.CodecString(frame.Header().Cid))
	}
	b.idrOnce.Do(func() {
		b.wg.Go(func() {
			timer := time.NewTimer(WaitForIDR)
			defer timer.Stop()
			select {
			case <-b.ctx.Done():
				return
			case <-timer.C:
				b.idrExpired.Store(true)
			}
		})
	})

	var idr []byte
	var nalus [][]byte
	codec.SplitFrameWithStartCode(frame.Data(), func(nalu []byte) bool {
		nalus = append(nalus, nalu)
		return true
	})
	for _, nalu := range nalus {
		if codec.H264NaluType(nalu) == codec.H264_NAL_I_SLICE && codec.IsH264IDRFrame(nalu) {
			idr = nalu
			break
		}
	}

	if b.format == types.FrameFormatUnknown {
		if len(idr) == 0 {
			frame.Decommission()
			if b.idrExpired.Load() {
				return fmt.Errorf("no IDR frame in first %f seconds", WaitForIDR.Seconds())
			}
			return nil
		}
		ff, err := DetectH264Format(idr)
		if err != nil {
			frame.Decommission()
			return fmt.Errorf("detect H.264 format failed: %w", err)
		}
		b.format = ff
		b.logger.Debug().Stringer("format", b.format).Msg("Detected H.264 format")
	}

	pos := b.circBuf.Write(frame)
	b.mtx.Lock()
	b.curPTS = frame.Header().Pts
	b.keyFrames.Drop(frame.Header().Pts)
	if len(idr) > 0 {
		b.keyFrames.Set(frame.Header().Pts, pos)
	}
	if !b.started {
		b.startPTS = frame.Header().Pts
		b.started = true
	}
	b.mtx.Unlock()
	if !b.IsReady() && frame.Header().Cid == codec.CODECID_VIDEO_H264 {
		b.mtx.RLock()
		healthy := frame.Header().Pts-b.startPTS > 2*time.Second
		enough := b.keyFrames.Len() >= MinKeyFrames
		b.mtx.RUnlock()
		if healthy && enough {
			b.ready.Store(true)
			b.logger.Debug().Msg("Video buffer ready")
		}
	}
	frame.Decommission()
	return nil
}

func (b *subBuffer) writeAudio(frame types.Frame) error {
	if frame.Header().Cid != codec.CODECID_AUDIO_AAC && frame.Header().Cid != codec.CODECID_AUDIO_MP3 {
		frame.Decommission()
		return fmt.Errorf("invalid audio codec: %s", codec.CodecString(frame.Header().Cid))
	}
	b.mtx.Lock()
	if !b.started {
		b.startPTS = frame.Header().Pts
		b.started = true
	}
	b.curPTS = frame.Header().Pts
	b.mtx.Unlock()
	b.circBuf.Write(frame)
	if b.bufType == types.BufferTypeAudio && !b.IsReady() {
		b.mtx.RLock()
		healthy := frame.Header().Pts-b.startPTS > 2*time.Second
		b.mtx.RUnlock()
		if healthy {
			b.ready.Store(true)
			b.logger.Debug().Msg("Audio buffer ready")
		}
	}
	frame.Decommission()
	return nil
}

func (b *subBuffer) SubscribePTS(upstreamCtx context.Context, desiredPTS time.Duration) ([]types.BufferOutput, error) {
	if !b.IsReady() {
		return nil, types.ErrBufferNotReady
	}

	startPos, possiblePTS, err := b.getStartPosFromPTS(desiredPTS)
	if err != nil {
		return nil, err
	}
	return b.subscribe(upstreamCtx, startPos, possiblePTS)
}

func (b *subBuffer) SubscribeOffset(upstreamCtx context.Context, offsetToLive time.Duration) ([]types.BufferOutput, error) {
	if !b.IsReady() {
		return nil, types.ErrBufferNotReady
	}

	startPos, possiblePTS, err := b.getClosestKeyFramePos(offsetToLive.Abs())
	if err != nil {
		return nil, err
	}
	return b.subscribe(upstreamCtx, startPos, possiblePTS)
}

// Subscribe allows a subscriber to read frames from the buffer starting from a position determined by the provided offset to live.
func (b *subBuffer) subscribe(upstreamCtx context.Context, startPos int, _ time.Duration) ([]types.BufferOutput, error) {
	ch, err := b.circBuf.ReadFromPos(startPos, upstreamCtx)
	if err != nil {
		return nil, err
	}

	first, ok := <-ch
	if !ok {
		return nil, errors.New("buffer closed while subscribing")
	}
	firstFramePTS := first.Header().Pts

	outCh := make(chan types.Frame, b.cap)
	b.wg.Go(func() {
		defer CloseAndDrain(outCh)

		preBuf := []types.Frame{first}
		lastPTS := firstFramePTS

		b.mtx.RLock()
		preDur := b.curPTS - firstFramePTS
		b.mtx.RUnlock()

		b.logger.Debug().Dur("prebuffer", preDur).Msg("Building prebuffer for new subscriber")

		dropLogger := newDropLogger(b.logger)
		var dropped uint64

		for (lastPTS - firstFramePTS).Abs() < preDur {
			select {
			case <-b.ctx.Done():
				return
			case <-upstreamCtx.Done():
				return
			case frame, ok := <-ch:
				if !ok {
					break
				}
				preBuf = append(preBuf, frame)
				lastPTS = frame.Header().Pts
			}
		}
		for _, frame := range preBuf {
			select {
			case <-b.ctx.Done():
				return
			case <-upstreamCtx.Done():
				return
			case outCh <- frame:
			default:
				dropped++
				dropLogger.Warn().Uint64("dropped_total", dropped).Msg("Subscriber full, dropping prebuffer frame")
				frame.Decommission()
			}
		}
		for {
			select {
			case <-b.ctx.Done():
				return
			case <-upstreamCtx.Done():
				return
			case frame, ok := <-ch:
				if !ok {
					return
				}
				select {
				case outCh <- frame:
				default:
					dropped++
					dropLogger.Warn().Uint64("dropped_total", dropped).Msg("Subscriber full, dropping live frame")
					frame.Decommission()
				}
			}
		}
	})
	return []types.BufferOutput{{
		Channel:  outCh,
		Type:     b.bufType,
		Title:    "PGM",
		StartPTS: firstFramePTS,
	}}, nil
}

func (b *subBuffer) Cancel() {
	b.cancel()
}

func (b *subBuffer) Close() {
	b.logger.Debug().Msg("Closing buffer stream")
	b.wg.Wait()
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.ready.Store(false)
	if b.circBuf != nil {
		b.circBuf.Close()
		b.circBuf = nil
	}
	b.started = false
	b.startPTS = 0
	b.curPTS = 0
	b.keyFrames = nil
	b.format = types.FrameFormatUnknown
}

// getClosestKeyFramePos finds the closest keyframe position to the target PTS, searching backwards from the newest keyframe.
func (b *subBuffer) getClosestKeyFramePos(offset time.Duration) (pos int, pts time.Duration, err error) {
	b.mtx.RLock()
	defer b.mtx.RUnlock()

	if b.keyFrames.Len() == 0 {
		return 0, 0, errors.New("no keyframes in buffer")
	}

	targetPTS := b.curPTS - offset
	keys := b.keyFrames.Keys()
	newestIdx := len(keys) - 1
	pts = keys[newestIdx]
	minDiff := (targetPTS - pts).Abs()
	for i := newestIdx - 1; i >= 0; i-- {
		k := keys[i]
		diff := (targetPTS - k).Abs()
		if diff <= minDiff {
			minDiff = diff
			pts = k
		} else {
			break
		}
	}
	pos, ok := b.keyFrames.Get(pts)
	if !ok {
		return 0, 0, fmt.Errorf("no keyframe for pts %s", pts)
	}
	return pos, pts, nil
}

// getStartPosFromPTS finds the closest frame position to the target PTS, searching backwards from the newest position.
func (b *subBuffer) getStartPosFromPTS(targetPTS time.Duration) (pos int, pts time.Duration, err error) {
	b.mtx.RLock()
	defer b.mtx.RUnlock()

	if targetPTS > b.curPTS {
		return 0, 0, fmt.Errorf("requested PTS '%s' is in the future relative to buffer current newest PTS '%s' in buffer", targetPTS, b.curPTS)
	}

	newestFramePos := (b.circBuf.writePos - 1 + b.cap) % b.cap
	peeked, err := b.circBuf.peek(newestFramePos)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to peek newest frame: %w", err)
	}
	minDiff := (targetPTS - peeked.Header().Pts).Abs()
	closestPos := newestFramePos
	closestPTS := peeked.Header().Pts
	for i := 1; i < b.cap; i++ {
		pos := (newestFramePos - i + b.cap) % b.cap // this handles wrap-around correctly and walks backwards through the buffer
		peekedFrame, err := b.circBuf.peek(pos)
		if err != nil {
			if errors.Is(err, ErrNoPayload) {
				break // buffer hasn't been fully filled yet, so the latest frame is the closest we can get
			}
			return 0, 0, fmt.Errorf("failed to peek frame at pos %d: %w", pos, err)
		}
		diff := (targetPTS - peekedFrame.Header().Pts).Abs()
		if diff <= minDiff {
			minDiff = diff
			closestPos = pos
			closestPTS = peekedFrame.Header().Pts
		} else {
			break
		}
	}
	return closestPos, closestPTS, nil
}
