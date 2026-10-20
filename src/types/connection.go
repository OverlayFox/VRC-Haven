package types

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/datarhei/gosrt/packet"
	"github.com/rs/zerolog"
)

type ConnectionType string

const (
	ConnectionTypeUnknown          ConnectionType = "unknown"
	ConnectionTypePublisher        ConnectionType = "publish"
	ConnectionTypeReader           ConnectionType = "read"
	ConnectionTypeEscort           ConnectionType = "escort"
	ConnectionTypePublishingEscort ConnectionType = "publishing_escort" // an escort connection that is also a publisher, i.e. it receives the stream from an upstream flagship and republishes it to downstream escorts/viewers
)

var connectionTypeStrings = map[string]ConnectionType{
	"unknown":           ConnectionTypeUnknown,
	"publish":           ConnectionTypePublisher,
	"read":              ConnectionTypeReader,
	"escort":            ConnectionTypeEscort,
	"publishing_escort": ConnectionTypePublishingEscort,
}

func (c ConnectionType) String() string {
	return string(c)
}

func (ct ConnectionType) MarshalJSON() ([]byte, error) {
	return json.Marshal(ct.String())
}

func (ct *ConnectionType) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	*ct = ConnectionTypeFromString(str)
	return nil
}

func ConnectionTypeFromString(s string) ConnectionType {
	if t, ok := connectionTypeStrings[s]; ok {
		return t
	}
	return ConnectionTypeUnknown
}

func (c ConnectionType) IsFlagship() bool {
	return c == ConnectionTypePublisher
}

type Connection interface {
	GetAddr() net.Addr
	GetType() ConnectionType
	GetCtx() context.Context
	GetLocation() Location
	GetLogger() zerolog.Logger

	Close()
}

type ConnectionSRT interface {
	Connection
	WritePacket(pkt packet.Packet) error
	Read() chan packet.Packet

	// GetAdvertisedAddr returns the host:port an escort serves HLS on, empty for other connection types.
	GetAdvertisedAddr() string
	GetStats() SRTStats
}

type ConnectionRTSP interface {
	Connection
	Write(stream []BufferOutput) error
	GetStream() *gortsplib.ServerStream
	StartPlay() error
	HandleHTTP(w http.ResponseWriter, r *http.Request)
}
