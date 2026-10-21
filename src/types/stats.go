package types

// SRTStats is a snapshot of an SRT connection's link health.
type SRTStats struct {
	ConnectedForMs uint64 `json:"connected_for_ms"`

	RTTMs            float64 `json:"rtt_ms"`
	SendMbps         float64 `json:"send_mbps"`
	RecvMbps         float64 `json:"recv_mbps"`
	LinkCapacityMbps float64 `json:"link_capacity_mbps"`
	SendLossPercent  float64 `json:"send_loss_percent"`
	RecvLossPercent  float64 `json:"recv_loss_percent"`
	SendBufMs        uint64  `json:"send_buf_ms"`
	RecvBufMs        uint64  `json:"recv_buf_ms"`
	LatencyMs        uint64  `json:"latency_ms"`

	PktSent        uint64 `json:"pkt_sent"`
	PktRecv        uint64 `json:"pkt_recv"`
	PktSendLoss    uint64 `json:"pkt_send_loss"`
	PktRecvLoss    uint64 `json:"pkt_recv_loss"`
	PktRetrans     uint64 `json:"pkt_retrans"`
	PktRecvRetrans uint64 `json:"pkt_recv_retrans"`
	PktSendDrop    uint64 `json:"pkt_send_drop"`
	PktRecvDrop    uint64 `json:"pkt_recv_drop"`
	ByteSent       uint64 `json:"byte_sent"`
	ByteRecv       uint64 `json:"byte_recv"`
}

// PeerStats describes one SRT peer of the haven, a publisher, upstream flagship or escort.
type PeerStats struct {
	Addr     string         `json:"addr"`
	Type     ConnectionType `json:"type"`
	Location Location       `json:"location"`
	// HLSAddr is where an escort serves HLS to redirected viewers, empty for other peers
	HLSAddr string   `json:"hls_addr,omitempty"`
	SRT     SRTStats `json:"srt"`
}
