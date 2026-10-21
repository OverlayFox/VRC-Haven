package hls

import (
	"cmp"
	"slices"
	"time"
)

// ViewerStatus is what a player is doing as far as its requests tell.
type ViewerStatus string

const (
	ViewerStatusStarting ViewerStatus = "starting" // requested the playlist but no segment yet
	ViewerStatusPlaying  ViewerStatus = "playing"
	ViewerStatusStuck    ViewerStatus = "stuck" // keeps reloading the playlist but stopped fetching new segments
	ViewerStatusIdle     ViewerStatus = "idle"  // stopped requesting, removed once viewerIdleTimeout passes
)

// RedirectStats counts the playlist requests a flagship sent to its escorts.
type RedirectStats struct {
	Total   uint64           `json:"total"`
	Escorts []EscortRedirect `json:"escorts"`
}

type EscortRedirect struct {
	HLSAddr string `json:"hls_addr"`
	Count   uint64 `json:"count"`
}

// Stats is a snapshot of the stream served over HLS and the players fetching it.
type Stats struct {
	Active             bool          `json:"active"` // a publisher feeds the playlist, started by the first viewer request
	Primed             bool          `json:"primed"` // the playlist is being served, also while it waits for the publisher to reconnect
	Ended              bool          `json:"ended"`  // the publisher did not reconnect in time and the playlist has EXT-X-ENDLIST
	TargetDurationS    int           `json:"target_duration_s"`
	NewestSeq          uint64        `json:"newest_seq"`
	SinceLastSegmentMs *int64        `json:"since_last_segment_ms"`
	Viewers            []ViewerStats `json:"viewers"`
}

type ViewerStats struct {
	ID        string       `json:"id"`
	IP        string       `json:"ip"`
	UserAgent string       `json:"user_agent"`
	Status    ViewerStatus `json:"status"`

	WatchedMs          int64   `json:"watched_ms"`
	SinceLastSeenMs    int64   `json:"since_last_seen_ms"`
	SinceLastSegmentMs *int64  `json:"since_last_segment_ms"`
	SegmentsBehindLive *uint64 `json:"segments_behind_live"`

	PlaylistRequests int `json:"playlist_requests"`
	SegmentRequests  int `json:"segment_requests"`
	SegmentsNotFound int `json:"segments_not_found"`
}

// RedirectStats returns how many playlist requests were redirected to each escort, busiest first.
func (s *Server) RedirectStats() RedirectStats {
	s.redirectMtx.Lock()
	defer s.redirectMtx.Unlock()

	stats := RedirectStats{Escorts: make([]EscortRedirect, 0, len(s.redirects))}
	for addr, count := range s.redirects {
		stats.Total += count
		stats.Escorts = append(stats.Escorts, EscortRedirect{HLSAddr: addr, Count: count})
	}
	slices.SortFunc(stats.Escorts, func(a, b EscortRedirect) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.HLSAddr, b.HLSAddr))
	})
	return stats
}

// Stats returns the state of the HLS stream and its viewers.
func (s *Server) Stats() Stats {
	s.connMtx.Lock()
	active := s.connection != nil
	s.connMtx.Unlock()

	return s.stream.stats(time.Now(), active)
}

func (s *liveStream) stats(now time.Time, active bool) Stats {
	stats := Stats{Active: active}

	s.mtx.RLock()
	select {
	case <-s.ready:
		stats.Primed = true
	default:
	}
	stats.Ended = s.playlist.ended
	stats.SinceLastSegmentMs = msSince(now, s.lastSegmentAt)
	info, hasInfo := s.playlist.info()
	s.mtx.RUnlock()

	if hasInfo {
		stats.TargetDurationS = info.targetDuration
		stats.NewestSeq = info.newestSeq
	}
	stats.Viewers = s.viewers.snapshot(now, info, hasInfo)
	return stats
}

// snapshot returns every tracked viewer, longest watching first. info is only valid if hasInfo is set.
func (t *viewerTracker) snapshot(now time.Time, info playlistInfo, hasInfo bool) []ViewerStats {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	viewers := make([]*viewer, 0, len(t.viewers))
	for _, v := range t.viewers {
		viewers = append(viewers, v)
	}
	slices.SortFunc(viewers, func(a, b *viewer) int {
		return cmp.Or(a.firstSeen.Compare(b.firstSeen), cmp.Compare(a.id, b.id))
	})

	stats := make([]ViewerStats, 0, len(viewers))
	for _, v := range viewers {
		vs := ViewerStats{
			ID:                 v.id,
			IP:                 v.ip,
			UserAgent:          v.userAgent,
			Status:             v.status(now),
			WatchedMs:          v.lastSeen.Sub(v.firstSeen).Milliseconds(),
			SinceLastSeenMs:    now.Sub(v.lastSeen).Milliseconds(),
			SinceLastSegmentMs: msSince(now, v.lastSegmentAt),
			PlaylistRequests:   v.playlistRequests,
			SegmentRequests:    v.segmentRequests,
			SegmentsNotFound:   v.notFound,
		}
		if hasInfo && v.segmentRequests > 0 && info.newestSeq >= v.lastSegmentSeq {
			behind := info.newestSeq - v.lastSegmentSeq
			vs.SegmentsBehindLive = &behind
		}
		stats = append(stats, vs)
	}
	return stats
}

func (v *viewer) status(now time.Time) ViewerStatus {
	switch {
	case now.Sub(v.lastSeen) > minStallTimeout:
		return ViewerStatusIdle
	case v.stuck:
		return ViewerStatusStuck
	case v.segmentRequests == 0:
		return ViewerStatusStarting
	default:
		return ViewerStatusPlaying
	}
}

// msSince returns the milliseconds since t, nil if t never happened.
func msSince(now, t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	ms := now.Sub(t).Milliseconds()
	return &ms
}
