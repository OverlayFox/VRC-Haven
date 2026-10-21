package buffer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Haven/src/buffer"
	"github.com/OverlayFox/VRC-Haven/src/types"
)

const frameInterval = 20 * time.Millisecond

func testVideoFrame(t *testing.T, i int, idr bool) types.Frame {
	t.Helper()
	data := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x00} // non-IDR slice
	if idr {
		data = []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00}
	}
	pts := time.Duration(i) * frameInterval
	frame, err := buffer.NewFrameFromData(types.FrameHeader{Cid: codec.CODECID_VIDEO_H264, Pts: pts, Dts: pts}, data)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func testAudioFrame(t *testing.T, i int) types.Frame {
	t.Helper()
	pts := time.Duration(i) * frameInterval
	frame, err := buffer.NewFrameFromData(types.FrameHeader{Cid: codec.CODECID_AUDIO_AAC, Pts: pts, Dts: pts}, []byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestKeyFrameIndexStaysWithinRing(t *testing.T) {
	b := buffer.NewBufferStream(zerolog.Nop(), types.BufferTypeVideo)
	defer b.Close()

	const gop = 10
	for i := range 5 * buffer.BufCap {
		if err := b.Write(testVideoFrame(t, i, i%gop == 0)); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := buffer.KeyFrameCount(b), buffer.BufCap/gop; got != want {
		t.Fatalf("keyframe index has %d entries, want the %d keyframes still in the ring", got, want)
	}
}

// TestSubscribeRacesWriteAndClose subscribes while frames are written and the buffer is closed, run it with -race.
func TestSubscribeRacesWriteAndClose(t *testing.T) {
	for range 10 {
		buf := buffer.NewBuffer(zerolog.Nop())

		i := 0
		write := func() {
			if err := buf.Write(testVideoFrame(t, i, i%10 == 0)); err != nil {
				t.Error(err)
			}
			if err := buf.Write(testAudioFrame(t, i)); err != nil {
				t.Error(err)
			}
			i++
		}
		for !buf.IsReady() {
			write()
		}

		writerCtx, stopWriter := context.WithCancel(context.Background())
		var writer sync.WaitGroup
		writer.Go(func() {
			for writerCtx.Err() == nil {
				write()
			}
		})

		var subscribers sync.WaitGroup
		for range 8 {
			subscribers.Go(func() {
				outputs, err := buf.Subscribe(context.Background(), -2*time.Second)
				if err != nil {
					return
				}
				for _, output := range outputs {
					for frame := range output.Channel {
						frame.Decommission()
					}
				}
			})
		}

		time.Sleep(10 * time.Millisecond)
		stopWriter()
		writer.Wait()
		buf.Close()

		done := make(chan struct{})
		go func() {
			subscribers.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("subscribers did not finish after the buffer closed")
		}
	}
}
