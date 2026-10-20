package srt

import (
	"errors"
	"fmt"
	"strings"

	goSrt "github.com/datarhei/gosrt"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

type streamRequest struct {
	streamID       string
	connectionType types.ConnectionType
}

func parseStreamRequest(req goSrt.ConnRequest) (streamRequest, error) {
	parts := strings.Split(req.StreamId(), ":")
	if len(parts) != 2 {
		return streamRequest{}, errors.New("invalid stream ID format")
	}
	connectionType := types.ConnectionTypeFromString(parts[0])
	if connectionType == types.ConnectionTypeUnknown {
		return streamRequest{}, fmt.Errorf("unknown connection type '%s'", parts[0])
	}

	return streamRequest{
		streamID:       parts[1],
		connectionType: connectionType,
	}, nil
}

func validateConnectionRequest(haven types.Haven, req goSrt.ConnRequest, streamID streamRequest) error {
	if req.Version() != 5 {
		req.Reject(goSrt.REJ_VERSION)
		return fmt.Errorf("unsupported SRT version '%d'", req.Version())
	}
	if !req.IsEncrypted() {
		req.Reject(goSrt.REJ_UNSECURE)
		return errors.New("connection is not encrypted")
	}
	if err := req.SetPassphrase(haven.GetPassphrase()); err != nil {
		req.Reject(goSrt.REJ_BADSECRET)
		return fmt.Errorf("failed to set passphrase: %w", err)
	}

	switch streamID.connectionType {
	case types.ConnectionTypePublisher:
		if _, err := haven.GetPublisher(); err == nil {
			req.Reject(goSrt.REJ_ROGUE)
			return errors.New("a publisher is already connected")
		}
	case types.ConnectionTypeEscort:
	default:
		req.Reject(goSrt.REJ_ROGUE)
		return fmt.Errorf("unsupported connection type '%s'", streamID.connectionType.String())
	}

	return nil
}
