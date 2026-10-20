package buffer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Stream-Haven/src/types"
)

var (
	ErrWrapAround   = errors.New("buffer wrapped, no frames available")
	ErrOutOfRange   = errors.New("read out of bounds")
	ErrNoSubscriber = errors.New("channel not subscribed")
	ErrNoPayload    = errors.New("no payload at position")
)

type circularBuffer struct {
	logger     zerolog.Logger
	dropLogger zerolog.Logger
	dropped    atomic.Uint64

	frames   []types.Frame
	writePos int
	cap      int

	subs   map[chan types.Frame]chan types.Frame
	subMtx sync.RWMutex

	mtx    sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newCircularBuffer(logger zerolog.Logger, capacity int) *circularBuffer {
	ctx, cancel := context.WithCancel(context.Background())
	return &circularBuffer{
		logger:     logger,
		dropLogger: newDropLogger(logger),
		frames:     make([]types.Frame, capacity),
		writePos:   0,
		cap:        capacity,
		subs:       make(map[chan types.Frame]chan types.Frame),
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (cb *circularBuffer) broadcast(frame types.Frame) {
	cb.subMtx.RLock()
	defer cb.subMtx.RUnlock()
	for _, liveCh := range cb.subs {
		cf := frame.Clone()
		select {
		case liveCh <- cf:
		case <-cb.ctx.Done():
			cf.Decommission()
			return
		default:
			cb.dropLogger.Warn().Uint64("dropped_total", cb.dropped.Add(1)).Msg("Subscriber live channel full, dropping frame")
			cf.Decommission()
		}
	}
}

func (cb *circularBuffer) removeSubscriber(ch chan types.Frame) error {
	cb.subMtx.Lock()
	defer cb.subMtx.Unlock()
	if liveCh, ok := cb.subs[ch]; ok {
		close(ch)
		CloseAndDrain(liveCh)
		delete(cb.subs, ch)
		return nil
	}
	return fmt.Errorf("not found: %w", ErrNoSubscriber)
}

func (cb *circularBuffer) Write(frame types.Frame) int {
	cb.mtx.Lock()
	defer cb.mtx.Unlock()

	clonedFrame := frame.Clone()

	if cb.frames[cb.writePos] != nil {
		cb.frames[cb.writePos].Decommission()
	}

	cb.frames[cb.writePos] = clonedFrame

	currentWritePos := cb.writePos
	cb.writePos = (cb.writePos + 1) % cb.cap

	cb.broadcast(clonedFrame)

	return currentWritePos
}

func (cb *circularBuffer) peek(pos int) (types.Frame, error) {
	cb.mtx.RLock()
	defer cb.mtx.RUnlock()

	if pos < 0 || pos >= cb.cap {
		return nil, ErrOutOfRange
	}

	frame := cb.frames[pos]
	if frame == nil {
		return nil, ErrNoPayload
	}

	return frame, nil
}

// ReadFromPos reads frames from the circular buffer starting from the given position.
func (cb *circularBuffer) ReadFromPos(startPos int, upstreamCtx context.Context) (chan types.Frame, error) {
	if startPos < 0 || startPos > cb.cap {
		return nil, fmt.Errorf("%w: '%d'", ErrOutOfRange, startPos)
	}
	ch := make(chan types.Frame, cb.cap)
	liveCh := make(chan types.Frame, cb.cap)
	cb.wg.Go(func() {
		defer func() {
			err := cb.removeSubscriber(ch)
			if err != nil {
				cb.logger.Error().Err(err).Msg("Failed to remove subscriber")
			}
		}()
		dropLogger := newDropLogger(cb.logger)
		var dropped uint64
		var history []types.Frame
		cb.mtx.RLock()
		for i := range cb.cap {
			pos := (startPos + i) % cb.cap
			if pos == cb.writePos {
				break
			}
			if frame := cb.frames[pos]; frame != nil {
				history = append(history, frame.Clone())
			}
		}
		cb.subMtx.Lock()
		cb.subs[ch] = liveCh
		cb.subMtx.Unlock()
		cb.mtx.RUnlock()
		for i, frame := range history {
			select {
			case <-cb.ctx.Done():
				frame.Decommission()
				for _, f := range history[i+1:] {
					f.Decommission()
				}
				return
			case <-upstreamCtx.Done():
				frame.Decommission()
				for _, f := range history[i+1:] {
					f.Decommission()
				}
				return
			case ch <- frame:
			default:
				dropped++
				dropLogger.Warn().Uint64("dropped_total", dropped).Msg("Egress channel full, dropping history frame")
				frame.Decommission()
			}
		}
		for {
			select {
			case <-cb.ctx.Done():
				return
			case <-upstreamCtx.Done():
				return
			case frame, ok := <-liveCh:
				if !ok {
					return
				}
				select {
				case ch <- frame:
				case <-cb.ctx.Done():
					frame.Decommission()
					return
				case <-upstreamCtx.Done():
					frame.Decommission()
					return
				default:
					dropped++
					dropLogger.Warn().Uint64("dropped_total", dropped).Msg("Egress channel full, dropping live frame")
					frame.Decommission()
				}
			}
		}
	})
	return ch, nil
}

func (cb *circularBuffer) Close() {
	cb.cancel()
	cb.wg.Wait()
	cb.mtx.Lock()
	defer cb.mtx.Unlock()
	cb.subMtx.Lock()
	defer cb.subMtx.Unlock()
	for ch, liveCh := range cb.subs {
		close(ch)
		delete(cb.subs, ch)
		CloseAndDrain(liveCh)
	}
	for _, frame := range cb.frames {
		if frame != nil {
			frame.Decommission()
		}
	}
}
