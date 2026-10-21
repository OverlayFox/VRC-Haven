package hls

import (
	"time"

	"github.com/rs/zerolog"
)

const (
	PublisherReconnectGrace = publisherReconnectGrace
	EndedPlaylistRetention  = endedPlaylistRetention
	PlaylistMinSegments     = playlistMinSegments
)

type (
	LivePlaylist = livePlaylist
	LiveStream   = liveStream
)

func NewLivePlaylist() *LivePlaylist {
	return newLivePlaylist()
}

func (p *LivePlaylist) AddSegment(duration time.Duration) uint64 {
	return p.addSegment(duration, nil)
}

func (p *LivePlaylist) MarkDiscontinuity() bool {
	return p.markDiscontinuity()
}

func (p *LivePlaylist) Rendered() string {
	return string(p.snapshot())
}

func (p *LivePlaylist) Filename(seq uint64) string {
	return p.filename(seq)
}

func (p *LivePlaylist) OldestSeq() uint64 {
	return p.segments[0].seq
}

func NewLiveStream() *LiveStream {
	return newLiveStream(zerolog.Nop())
}

func (s *LiveStream) BeginFeed() {
	s.beginFeed()
}

func (s *LiveStream) EndFeed() {
	s.endFeed()
}

func (s *LiveStream) AddSegment(duration time.Duration) uint64 {
	return s.addSegment(duration, nil)
}

func (s *LiveStream) Tick(now time.Time) {
	s.tick(now)
}

func (s *LiveStream) IdleSince() time.Time {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.idleSince
}

// Playlist returns the current playlist, which a reset replaces.
func (s *LiveStream) Playlist() *LivePlaylist {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.playlist
}

func (s *LiveStream) Ready() bool {
	select {
	case <-s.readyCh():
		return true
	default:
		return false
	}
}
