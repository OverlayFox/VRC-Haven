package hls

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	monitorInterval   = 1 * time.Second
	statsInterval     = 10 * time.Second
	viewerIdleTimeout = 15 * time.Second
	minStallTimeout   = 5 * time.Second
	slowMediaRate     = 0.9
)

// stallTimeout returns how long the playlist or a viewer may go without progress.
func stallTimeout(targetDuration int) time.Duration {
	return max(minStallTimeout, 3*time.Duration(targetDuration)*time.Second)
}

func durSince(e *zerolog.Event, key string, now, t time.Time) *zerolog.Event {
	if t.IsZero() {
		return e.Str(key, "never")
	}
	return e.Dur(key, now.Sub(t))
}

// viewer is one player, identified by IP and user agent since players open new TCP connections freely.
type viewer struct {
	ip        string
	userAgent string

	firstSeen      time.Time
	lastSeen       time.Time
	lastPlaylistAt time.Time
	lastSegmentAt  time.Time

	playlistRequests int
	segmentRequests  int
	notFound         int
	lastMediaSeq     uint64
	lastSegmentSeq   uint64

	// stuck is set while the viewer reloads the playlist without fetching new segments
	stuck bool
}

func (v *viewer) log(e *zerolog.Event, now time.Time) *zerolog.Event {
	e = e.Str("client_ip", v.ip).
		Str("user_agent", v.userAgent).
		Dur("watched", v.lastSeen.Sub(v.firstSeen)).
		Int("playlist_requests", v.playlistRequests).
		Int("segment_requests", v.segmentRequests).
		Int("segments_not_found", v.notFound).
		Uint64("last_media_seq", v.lastMediaSeq).
		Uint64("last_segment_seq", v.lastSegmentSeq)
	e = durSince(e, "since_last_playlist", now, v.lastPlaylistAt)
	return durSince(e, "since_last_segment", now, v.lastSegmentAt)
}

// viewerTracker follows what each player requests, so logs show whether a stuck player stopped requesting entirely, keeps reloading a playlist or hits missing segments.
type viewerTracker struct {
	logger  zerolog.Logger
	mtx     sync.Mutex
	viewers map[string]*viewer
}

func newViewerTracker(logger zerolog.Logger) *viewerTracker {
	return &viewerTracker{
		logger:  logger,
		viewers: make(map[string]*viewer),
	}
}

// get returns the viewer behind r, the caller must hold mtx.
func (t *viewerTracker) get(r *http.Request, now time.Time) *viewer {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	userAgent := r.UserAgent()

	key := ip + " " + userAgent
	v, ok := t.viewers[key]
	if !ok {
		v = &viewer{ip: ip, userAgent: userAgent, firstSeen: now}
		t.viewers[key] = v
		t.logger.Info().Str("client_ip", ip).Str("user_agent", userAgent).Int("viewers", len(t.viewers)).Msg("New HLS viewer")
	}
	v.lastSeen = now
	return v
}

func (t *viewerTracker) playlistServed(r *http.Request, info playlistInfo) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	now := time.Now()
	v := t.get(r, now)
	v.playlistRequests++
	v.lastPlaylistAt = now
	v.lastMediaSeq = info.mediaSeq
}

func (t *viewerTracker) segmentServed(r *http.Request, seq uint64) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	now := time.Now()
	v := t.get(r, now)
	v.segmentRequests++
	v.lastSegmentAt = now
	v.lastSegmentSeq = seq
}

func (t *viewerTracker) segmentNotFound(r *http.Request) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	t.get(r, time.Now()).notFound++
}

func (t *viewerTracker) count() int {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	return len(t.viewers)
}

// check reports viewers that left or got stuck. info is only valid if hasInfo is set.
func (t *viewerTracker) check(now time.Time, info playlistInfo, hasInfo bool) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	stall := stallTimeout(info.targetDuration)
	for key, v := range t.viewers {
		if now.Sub(v.lastSeen) > viewerIdleTimeout {
			event := t.logger.Info()
			if hasInfo && v.segmentRequests > 0 && info.newestSeq >= v.lastSegmentSeq {
				event = event.Uint64("segments_behind_live", info.newestSeq-v.lastSegmentSeq)
			}
			v.log(event, now).Int("viewers", len(t.viewers)-1).Msg("HLS viewer stopped requesting")
			delete(t.viewers, key)
			continue
		}

		lastProgress := v.lastSegmentAt
		if lastProgress.IsZero() {
			lastProgress = v.firstSeen
		}
		polling := !v.lastPlaylistAt.IsZero() && now.Sub(v.lastPlaylistAt) < stall
		newSegments := hasInfo && (v.segmentRequests == 0 || info.newestSeq > v.lastSegmentSeq)
		stuck := polling && newSegments && now.Sub(lastProgress) > stall

		switch {
		case stuck && !v.stuck:
			event := t.logger.Warn()
			if hasInfo {
				event = event.Uint64("newest_seq", info.newestSeq)
			}
			v.log(event, now).Msg("HLS viewer keeps reloading the playlist but stopped fetching new segments")
		case !stuck && v.stuck:
			v.log(t.logger.Info(), now).Msg("HLS viewer is fetching segments again")
		}
		v.stuck = stuck
	}
}

// monitor periodically reports whether the playlist keeps advancing, where media stopped if it does not, and what viewers do.
func (c *Connection) monitor() {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	var stalledSince time.Time
	var lastStats streamHealth
	lastStatsAt := time.Now()

	for {
		var now time.Time
		select {
		case <-c.ctx.Done():
			return
		case now = <-ticker.C:
		}

		c.mtx.RLock()
		health := c.seg.streamHealth()
		info, hasInfo := c.seg.playlistInfo()
		c.mtx.RUnlock()

		c.viewers.check(now, info, hasInfo)

		if hasInfo {
			sinceSegment := now.Sub(health.lastSegmentAt)
			stall := stallTimeout(info.targetDuration)
			switch {
			case sinceSegment > stall && stalledSince.IsZero():
				stalledSince = health.lastSegmentAt
				// Which of these is old tells whether the publisher, the keyframes or the muxer stopped
				event := c.logger.Warn().Dur("since_last_segment", sinceSegment).Uint64("newest_seq", info.newestSeq)
				event = durSince(event, "since_last_video_frame", now, health.lastVideoAt)
				event = durSince(event, "since_last_keyframe", now, health.lastKeyFrameAt)
				event = durSince(event, "since_last_audio_frame", now, health.lastAudioAt)
				event.Msg("HLS playlist stopped advancing")
			case sinceSegment <= stall && !stalledSince.IsZero():
				c.logger.Info().Dur("stalled_for", health.lastSegmentAt.Sub(stalledSince)).Msg("HLS playlist is advancing again")
				stalledSince = time.Time{}
			}
		}

		if elapsed := now.Sub(lastStatsAt); elapsed >= statsInterval {
			c.logStats(health, lastStats, elapsed, info)
			lastStats = health
			lastStatsAt = now
		}
	}
}

func (c *Connection) logStats(health, last streamHealth, elapsed time.Duration, info playlistInfo) {
	seconds := elapsed.Seconds()
	event := c.logger.Debug().
		Float64("video_fps", float64(health.videoFrames-last.videoFrames)/seconds).
		Float64("audio_frames_per_sec", float64(health.audioFrames-last.audioFrames)/seconds).
		Uint64("segments_added", health.segments-last.segments).
		Uint64("newest_seq", info.newestSeq).
		Int("target_duration", info.targetDuration).
		Int("viewers", c.viewers.count())

	if last.videoFrames > 0 && health.videoFrames > last.videoFrames {
		// 1.0 means media arrives in real time, below that players slowly run out of buffer
		mediaRate := (health.lastVideoPTS - last.lastVideoPTS).Seconds() / seconds
		event = event.Float64("media_rate", mediaRate)
		if mediaRate < slowMediaRate {
			c.logger.Warn().Float64("media_rate", mediaRate).Msg("Stream arrives slower than real time, players will run out of buffer")
		}
	}
	event.Msg("HLS stream stats")
}
