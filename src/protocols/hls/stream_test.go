package hls_test

import (
	"strings"
	"testing"
	"time"

	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
)

func addSegments(p *hls.LivePlaylist, n int) {
	for range n {
		p.AddSegment(time.Second)
	}
}

func feedSegments(s *hls.LiveStream, n int) {
	for range n {
		s.AddSegment(time.Second)
	}
}

func TestPlaylistWithoutReconnect(t *testing.T) {
	p := hls.NewLivePlaylist()
	addSegments(p, 10)

	rendered := p.Rendered()
	for _, tag := range []string{"#EXT-X-DISCONTINUITY", "#EXT-X-ENDLIST"} {
		if strings.Contains(rendered, tag) {
			t.Errorf("playlist without reconnect contains %s:\n%s", tag, rendered)
		}
	}
}

func TestPlaylistDiscontinuity(t *testing.T) {
	p := hls.NewLivePlaylist()
	addSegments(p, 3)
	if !p.MarkDiscontinuity() {
		t.Fatal("MarkDiscontinuity() = false with segments in the playlist")
	}
	if seq := p.AddSegment(time.Second); seq != 3 {
		t.Errorf("first segment after reconnect got seq %d, want 3", seq)
	}

	rendered := p.Rendered()
	want := "#EXT-X-DISCONTINUITY\n#EXTINF:1.000,\n" + p.Filename(3) + "\n"
	if !strings.Contains(rendered, want) {
		t.Errorf("playlist does not tag the reconnect, want %q in:\n%s", want, rendered)
	}
	if strings.Count(rendered, "#EXT-X-DISCONTINUITY\n") != 1 {
		t.Errorf("want exactly one discontinuity:\n%s", rendered)
	}
	if strings.Contains(rendered, "#EXT-X-DISCONTINUITY-SEQUENCE") {
		t.Errorf("discontinuity sequence set while the tagged segment is listed:\n%s", rendered)
	}

	// Once the tagged segment leaves the window, the discontinuity sequence keeps counting it, also after it is no longer retained
	for range 30 {
		p.AddSegment(time.Second)
		rendered = p.Rendered()
		if strings.Contains(rendered, "#EXT-X-DISCONTINUITY\n") {
			continue
		}
		if !strings.Contains(rendered, "#EXT-X-DISCONTINUITY-SEQUENCE:1\n") {
			t.Fatalf("want discontinuity sequence 1 once the tagged segment left the window:\n%s", rendered)
		}
	}
	if p.OldestSeq() <= 3 {
		t.Fatal("tagged segment still retained, test did not slide far enough")
	}
}

func TestStreamEndsWhenPublisherDoesNotReconnect(t *testing.T) {
	s := hls.NewLiveStream()
	s.BeginFeed()
	feedSegments(s, hls.PlaylistMinSegments)
	s.EndFeed()
	idleSince := s.IdleSince()

	s.Tick(idleSince.Add(hls.PublisherReconnectGrace - time.Second))
	if strings.Contains(s.Playlist().Rendered(), "#EXT-X-ENDLIST") {
		t.Fatal("playlist ended before the reconnect grace passed")
	}

	s.Tick(idleSince.Add(hls.PublisherReconnectGrace))
	if rendered := s.Playlist().Rendered(); !strings.HasSuffix(rendered, "#EXT-X-ENDLIST\n") {
		t.Fatalf("playlist not ended after the reconnect grace:\n%s", rendered)
	}

	s.Tick(idleSince.Add(hls.PublisherReconnectGrace + hls.EndedPlaylistRetention))
	if s.Ready() || s.Playlist().Rendered() != "" {
		t.Fatal("ended playlist not discarded after the retention")
	}
}

func TestStreamContinuesWhenPublisherReconnects(t *testing.T) {
	s := hls.NewLiveStream()
	s.BeginFeed()
	feedSegments(s, hls.PlaylistMinSegments)
	s.EndFeed()
	playlist := s.Playlist()

	s.BeginFeed()
	if seq := s.AddSegment(time.Second); seq != hls.PlaylistMinSegments {
		t.Errorf("reconnected publisher's first segment got seq %d, want %d", seq, hls.PlaylistMinSegments)
	}
	if s.Playlist() != playlist {
		t.Error("reconnect started a new playlist")
	}
	if !s.Ready() {
		t.Error("stream not ready after reconnect")
	}
	if rendered := playlist.Rendered(); !strings.Contains(rendered, "#EXT-X-DISCONTINUITY\n") {
		t.Errorf("reconnect not tagged:\n%s", rendered)
	}
}

func TestStreamRestartsAfterEnded(t *testing.T) {
	s := hls.NewLiveStream()
	s.BeginFeed()
	feedSegments(s, hls.PlaylistMinSegments)
	s.EndFeed()
	s.Tick(s.IdleSince().Add(hls.PublisherReconnectGrace))
	ended := s.Playlist()

	s.BeginFeed()
	if s.Playlist() == ended || s.Ready() {
		t.Error("publisher after an ended playlist did not start a new one")
	}
	if seq := s.AddSegment(time.Second); seq != 0 {
		t.Errorf("new playlist's first segment got seq %d, want 0", seq)
	}
}
