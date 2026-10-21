package types

import "net"

type Haven interface {
	GetStreamID() string
	GetPassphrase() string
	GetPublisher() (ConnectionSRT, error)

	AddConnection(Connection) error

	GetClosestEscort(Location) ConnectionSRT
	GetViewer(net.Addr) (ConnectionRTSP, error)

	// PublisherStats returns the stream's source, the publisher on a flagship or the upstream flagship on an escort. Nil while none is connected.
	PublisherStats() *PeerStats
	EscortStats() []PeerStats

	Close()
}
