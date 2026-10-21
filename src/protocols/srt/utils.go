package srt

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	goSrt "github.com/datarhei/gosrt"

	"github.com/OverlayFox/VRC-Haven/src/types"
)

type streamRequest struct {
	streamID       string
	connectionType types.ConnectionType
	// advertisedAddr is the host:port an escort serves HLS on, the host may be empty to use the escort's SRT address
	advertisedAddr string
}

// EscortStreamID builds the SRT stream ID an escort dials its flagship with.
func EscortStreamID(streamID, advertisedAddr string) string {
	return fmt.Sprintf("%s:%s:%s", types.ConnectionTypeEscort, streamID, advertisedAddr)
}

func parseStreamRequest(req goSrt.ConnRequest) (streamRequest, error) {
	// The advertised address keeps any further colons, e.g. of an IPv6 host
	parts := strings.SplitN(req.StreamId(), ":", 3)
	if len(parts) < 2 {
		return streamRequest{}, errors.New("invalid stream ID format")
	}
	connectionType := types.ConnectionTypeFromString(parts[0])
	if connectionType == types.ConnectionTypeUnknown {
		return streamRequest{}, fmt.Errorf("unknown connection type '%s'", parts[0])
	}

	request := streamRequest{
		streamID:       parts[1],
		connectionType: connectionType,
	}
	if len(parts) == 3 {
		request.advertisedAddr = parts[2]
	}
	return request, nil
}

// resolveAdvertisedAddr validates the HLS address an escort advertised and fills in its SRT host if it only advertised a port.
func resolveAdvertisedAddr(advertised string, remote net.Addr) (string, error) {
	if advertised == "" {
		return "", errors.New("escort did not advertise its HLS address")
	}
	host, port, err := net.SplitHostPort(advertised)
	if err != nil {
		return "", fmt.Errorf("invalid advertised HLS address '%s': %w", advertised, err)
	}
	if p, atoiErr := strconv.Atoi(port); atoiErr != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("invalid advertised HLS port '%s'", port)
	}

	if host == "" {
		host, _, err = net.SplitHostPort(remote.String())
		if err != nil {
			return "", fmt.Errorf("failed to get escort host from '%s': %w", remote.String(), err)
		}
	}
	if net.ParseIP(host) == nil && !isHostname(host) {
		return "", fmt.Errorf("invalid advertised HLS host '%s'", host)
	}

	return net.JoinHostPort(host, port), nil
}

// isHostname reports whether host is a DNS name, so an advertised address cannot smuggle a path or userinfo into redirect URLs.
func isHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
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
