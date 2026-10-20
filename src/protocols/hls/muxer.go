package hls

import (
	"context"
	"io"

	"github.com/asticode/go-astits"
)

const (
	videoPID uint16 = 0x100
	audioPID uint16 = 0x101

	// tsClockOffset shifts all PTS/DTS ahead of the PCR (90kHz ticks). With PCR == DTS
	// every frame is "due" the instant it arrives, which strict hardware/TV decoders
	// treat as buffer underflow, and audio lagging video would be late. It adds no
	// playback latency since HLS players buffer by segment, not by PCR. Same as ffmpeg's
	// default mux delay.
	tsClockOffset = 90000 * 7 / 10

	naluTypeSPS = 7
	naluTypePPS = 8
	naluTypeAUD = 9
)

var (
	annexBStartCode = []byte{0x00, 0x00, 0x00, 0x01}
	audNALU         = []byte{naluTypeAUD, 0xF0}
)

type segmentWriter struct {
	w io.Writer
}

func (s *segmentWriter) Write(p []byte) (int, error) {
	return s.w.Write(p)
}

type tsMuxer struct {
	mux      *astits.Muxer
	out      *segmentWriter
	sps, pps []byte
}

func newTSMuxer(ctx context.Context, sps, pps []byte) (*tsMuxer, error) {
	out := &segmentWriter{w: io.Discard}
	mux := astits.NewMuxer(ctx, out)

	if err := mux.AddElementaryStream(astits.PMTElementaryStream{
		ElementaryPID: videoPID,
		StreamType:    astits.StreamTypeH264Video,
	}); err != nil {
		return nil, err
	}
	if err := mux.AddElementaryStream(astits.PMTElementaryStream{
		ElementaryPID: audioPID,
		StreamType:    astits.StreamTypeAACAudio,
	}); err != nil {
		return nil, err
	}
	mux.SetPCRPID(videoPID)

	return &tsMuxer{mux: mux, out: out, sps: sps, pps: pps}, nil
}

func (m *tsMuxer) setOutput(w io.Writer) {
	m.out.w = w
}

func (m *tsMuxer) writeVideoAccessUnit(nalus [][]byte, ptsTicks, dtsTicks int64, keyFrame bool) error {
	// Keep the cached parameter sets current in case the encoder reconfigures mid-stream
	for _, nalu := range nalus {
		switch nalu[0] & 0x1F {
		case naluTypeSPS:
			m.sps = nalu
		case naluTypePPS:
			m.pps = nalu
		}
	}

	size := len(annexBStartCode) + len(audNALU)
	if keyFrame {
		size += 2*len(annexBStartCode) + len(m.sps) + len(m.pps)
	}
	for _, nalu := range nalus {
		size += len(annexBStartCode) + len(nalu)
	}

	// Canonical order: AUD, SPS, PPS, then the rest (SEI, slices)
	payload := make([]byte, 0, size)
	payload = appendNALU(payload, audNALU)
	if keyFrame {
		payload = appendNALU(payload, m.sps)
		payload = appendNALU(payload, m.pps)
	}
	for _, nalu := range nalus {
		switch nalu[0] & 0x1F {
		case naluTypeAUD:
			continue
		case naluTypeSPS, naluTypePPS:
			if keyFrame {
				continue // already written above
			}
		}
		payload = appendNALU(payload, nalu)
	}

	adaptationField := &astits.PacketAdaptationField{
		HasPCR:                true,
		PCR:                   &astits.ClockReference{Base: dtsTicks},
		RandomAccessIndicator: keyFrame,
	}

	optionalHeader := &astits.PESOptionalHeader{
		PTSDTSIndicator:        astits.PTSDTSIndicatorOnlyPTS,
		PTS:                    &astits.ClockReference{Base: ptsTicks + tsClockOffset},
		DataAlignmentIndicator: true,
	}
	if dtsTicks != ptsTicks {
		optionalHeader.PTSDTSIndicator = astits.PTSDTSIndicatorBothPresent
		optionalHeader.DTS = &astits.ClockReference{Base: dtsTicks + tsClockOffset}
	}

	_, err := m.mux.WriteData(&astits.MuxerData{
		PID:             videoPID,
		AdaptationField: adaptationField,
		PES: &astits.PESData{
			Data: payload,
			Header: &astits.PESHeader{
				OptionalHeader: optionalHeader,
			},
		},
	})
	return err
}

// writeAudioAccessUnit writes one or more concatenated ADTS frames as a single PES,
// the PTS refers to the first frame.
func (m *tsMuxer) writeAudioAccessUnit(adts []byte, ptsTicks int64) error {
	_, err := m.mux.WriteData(&astits.MuxerData{
		PID: audioPID,
		// Every AAC frame is a random access point.
		// Without the flag, demuxers that wait for one (Media Foundation, which VRChat uses on PC) never start the audio stream,
		// so the audio-driven clock never runs and video freezes on its first frame.
		AdaptationField: &astits.PacketAdaptationField{RandomAccessIndicator: true},
		PES: &astits.PESData{
			Data: adts,
			Header: &astits.PESHeader{
				OptionalHeader: &astits.PESOptionalHeader{
					PTSDTSIndicator:        astits.PTSDTSIndicatorOnlyPTS,
					PTS:                    &astits.ClockReference{Base: ptsTicks + tsClockOffset},
					DataAlignmentIndicator: true,
				},
			},
		},
	})
	return err
}

func appendNALU(dst, nalu []byte) []byte {
	dst = append(dst, annexBStartCode...)
	return append(dst, nalu...)
}
