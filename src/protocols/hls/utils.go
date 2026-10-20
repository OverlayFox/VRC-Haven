package hls

import (
	"errors"
	"strings"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/yapingcat/gomedia/go-codec"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

var ErrSPSPPSNotFound = errors.New("SPS/PPS not found in video frame")

// ExtractSPSPPS takes a video frame and extracts the SPS and PPS NAL units from it.
func ExtractSPSPPS(frame types.Frame) (sps, pps []byte, err error) {
	codec.SplitFrame(frame.Data(), func(nalu []byte) bool {
		if len(nalu) == 0 {
			return true
		}
		naluType := nalu[0] & 0x1F

		switch naluType {
		case 7: // SPS
			sps = make([]byte, len(nalu))
			copy(sps, nalu)
		case 8: // PPS
			pps = make([]byte, len(nalu))
			copy(pps, nalu)
		}

		return len(sps) == 0 || len(pps) == 0
	})

	if len(sps) == 0 || len(pps) == 0 {
		return nil, nil, ErrSPSPPSNotFound
	}

	return sps, pps, nil
}

func ExtractASC(frame types.Frame) (asc *mpeg4audio.AudioSpecificConfig, err error) {
	if frame.Header().Cid != codec.CODECID_AUDIO_AAC {
		return nil, errors.New("frame is not AAC audio")
	}

	data := frame.Data()
	if len(data) < 7 {
		return nil, errors.New("audio frame too short to contain valid ADTS header")
	}

	if data[0] != 0xFF || (data[1]&0xF0) != 0xF0 {
		return nil, errors.New("not an ADTS frame")
	}

	profile := ((data[2] >> 6) & 0x3) + 1 // AAC object type
	sampleRateIdx := (data[2] >> 2) & 0xF
	channelConfig := ((data[2] & 0x1) << 2) | ((data[3] >> 6) & 0x3)
	sampleRates := []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

	return &mpeg4audio.AudioSpecificConfig{
		Type:          mpeg4audio.ObjectType(profile),
		SampleRate:    sampleRates[sampleRateIdx],
		ChannelConfig: channelConfig,
	}, nil
}

func GetCredentials(path string) (streamID, passphrase string, err error) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return "", "", errors.New("invalid path format, expected /streamID/passphrase")
	}

	streamID = strings.TrimSpace(parts[1])
	passphrase = strings.TrimSpace(parts[2])
	return streamID, passphrase, nil
}
