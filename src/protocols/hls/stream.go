package hls

import (
	"bytes"
	"net/http"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	playlistWaitTimeout = 7 * time.Second

	// publisherReconnectGrace is how long players keep polling the unchanged playlist after the publisher left,
	// so a quick reconnect continues it without players reloading. Afterwards it is ended with EXT-X-ENDLIST.
	publisherReconnectGrace = 30 * time.Second

	// endedPlaylistRetention is how long an ended playlist is still served, so polling players see EXT-X-ENDLIST.
	// Afterwards players loading the stream get an error to retry on, instead of replaying the stale segments.
	endedPlaylistRetention = 10 * time.Second

	// noStreamRetryAfter matches VRChat's limit of one video URL load per 5 seconds.
	noStreamRetryAfter = "5"
)

// liveStream is the playlist players follow. It outlives publishers: a reconnecting publisher continues it after an EXT-X-DISCONTINUITY,
// so players keep polling one playlist instead of freezing on errors or on a media sequence that went backwards.
type liveStream struct {
	logger  zerolog.Logger
	viewers *viewerTracker

	mtx           sync.RWMutex
	playlist      *livePlaylist
	ready         chan struct{} // closed once the playlist has enough segments to serve
	lastSegmentAt time.Time

	feeding   bool
	idleSince time.Time // when the last publisher left, zero while one feeds the stream
	endedAt   time.Time
}

func newLiveStream(logger zerolog.Logger) *liveStream {
	return &liveStream{
		logger:   logger,
		viewers:  newViewerTracker(logger),
		playlist: newLivePlaylist(),
		ready:    make(chan struct{}),
	}
}

// beginFeed is called once a publisher's muxer is primed, its segments continue the playlist after a discontinuity.
func (s *liveStream) beginFeed() {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if s.playlist.ended {
		s.reset()
	}
	if s.playlist.markDiscontinuity() {
		s.logger.Info().Dur("publisher_gone_for", time.Since(s.idleSince)).Msg("Publisher is back, continuing the HLS playlist after a discontinuity")
	}
	s.feeding = true
	s.idleSince = time.Time{}
}

// endFeed is called once a publisher's connection stopped writing segments.
func (s *liveStream) endFeed() {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if !s.feeding {
		return
	}
	s.feeding = false
	s.idleSince = time.Now()
	if s.playlist.ready() {
		s.logger.Info().Dur("grace", publisherReconnectGrace).Msg("Publisher left, holding the HLS playlist for it to reconnect")
	}
}

// addSegment appends a finished segment and returns the sequence it was given.
func (s *liveStream) addSegment(duration time.Duration, data []byte) uint64 {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	seq := s.playlist.addSegment(duration, data)
	s.lastSegmentAt = time.Now()

	select {
	case <-s.ready:
	default:
		if s.playlist.ready() {
			s.logger.Info().Msg("HLS stream primed, serving playlist")
			close(s.ready)
		}
	}
	return seq
}

// tick ends the playlist once the publisher did not come back in time and drops it once players had the chance to see that.
func (s *liveStream) tick(now time.Time) {
	s.mtx.Lock()
	switch {
	case !s.feeding && !s.idleSince.IsZero() && !s.playlist.ended && s.playlist.ready() && now.Sub(s.idleSince) >= publisherReconnectGrace:
		s.playlist.end()
		s.endedAt = now
		s.logger.Info().Dur("publisher_gone_for", now.Sub(s.idleSince)).Msg("Publisher did not reconnect, ended the HLS playlist")
	case s.playlist.ended && now.Sub(s.endedAt) >= endedPlaylistRetention:
		s.reset()
		s.logger.Info().Msg("Discarded the ended HLS playlist")
	}
	info, hasInfo := s.playlist.info()
	s.mtx.Unlock()

	s.viewers.check(now, info, hasInfo)
}

// reset starts a new playlist with a new session, the caller must hold mtx.
func (s *liveStream) reset() {
	s.playlist = newLivePlaylist()
	s.ready = make(chan struct{})
	s.lastSegmentAt = time.Time{}
	s.endedAt = time.Time{}
}

func (s *liveStream) info() (playlistInfo, bool) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return s.playlist.info()
}

func (s *liveStream) readyCh() <-chan struct{} {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return s.ready
}

// handleHTTP serves the playlist and its in-RAM MPEG-TS segments to HTTP clients. conn is the publisher's connection feeding the stream, nil while there is none.
// Responses are written without holding the lock, so slow or distant viewers never stall muxing.
func (s *liveStream) handleHTTP(w http.ResponseWriter, r *http.Request, conn *Connection) {
	filename := path.Base(r.URL.Path)

	if filename == playlistFilename {
		s.servePlaylist(w, r, conn)
		return
	}

	s.mtx.RLock()
	seq, isSegment := s.playlist.parseFilename(filename)
	var data []byte
	ok := false
	if isSegment {
		data, ok = s.playlist.segment(seq)
	}
	info, hasInfo := s.playlist.info()
	s.mtx.RUnlock()

	if !ok {
		if notFound, shouldLog := s.viewers.segmentNotFound(r); shouldLog {
			s.logSegmentNotFound(r, filename, notFound, seq, isSegment, info, hasInfo)
		}
		http.NotFound(w, r)
		return
	}
	s.viewers.segmentServed(r, seq)

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "public, max-age=60")
	serveBytes(w, r, data)
}

// servePlaylist holds the request until the stream is primed instead of failing it, since many players give up on the first error.
func (s *liveStream) servePlaylist(w http.ResponseWriter, r *http.Request, conn *Connection) {
	ready := s.readyCh()
	select {
	case <-ready:
	default:
		if conn == nil {
			logClient(s.logger.Debug(), r).Msg("Playlist requested while no publisher is live")
			w.Header().Set("Retry-After", noStreamRetryAfter)
			http.Error(w, "No stream", http.StatusServiceUnavailable)
			return
		}
		if !s.waitReady(w, r, conn, ready) {
			return
		}
	}

	s.mtx.RLock()
	data := s.playlist.snapshot()
	info, _ := s.playlist.info()
	primed := s.playlist.ready()
	s.mtx.RUnlock()

	if !primed {
		// The ended playlist was discarded between the ready check and now
		w.Header().Set("Retry-After", noStreamRetryAfter)
		http.Error(w, "No stream", http.StatusServiceUnavailable)
		return
	}

	s.viewers.playlistServed(r, info)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	serveBytes(w, r, data)
}

// waitReady waits for the stream to prime and answers the request itself if it does not.
func (s *liveStream) waitReady(w http.ResponseWriter, r *http.Request, conn *Connection, ready <-chan struct{}) bool {
	timer := time.NewTimer(playlistWaitTimeout)
	defer timer.Stop()

	select {
	case <-ready:
		return true
	case <-r.Context().Done():
		logClient(s.logger.Info(), r).Msg("Client gave up on playlist request while the stream was priming")
	case <-conn.GetCtx().Done():
		logClient(s.logger.Info(), r).Msg("Publisher left while the stream was priming")
		w.Header().Set("Retry-After", noStreamRetryAfter)
		http.Error(w, "Stream ended", http.StatusServiceUnavailable)
	case <-timer.C:
		logClient(s.logger.Warn(), r).
			Bool("muxer_primed", conn.muxerPrimed()).
			Dur("since_connection_created", time.Since(conn.createdAt)).
			Msg("Playlist request timed out waiting for the stream to prime")
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Stream not primed yet", http.StatusServiceUnavailable)
	}
	return false
}

func (s *liveStream) logSegmentNotFound(r *http.Request, filename string, notFound int, seq uint64, isSegment bool, info playlistInfo, hasInfo bool) {
	event := logClient(s.logger.Warn(), r).Str("file", filename).Int("segments_not_found", notFound)
	switch {
	case !isSegment:
		event.Msg("Unknown HLS file requested") // Usually a segment of an earlier stream session from a cached playlist
		return
	case !hasInfo:
		event.Msg("HLS segment requested before any segment exists")
		return
	}

	event = event.Uint64("oldest_seq", info.oldestSeq).Uint64("newest_seq", info.newestSeq)
	switch {
	case seq < info.oldestSeq:
		event.Uint64("segments_behind", info.oldestSeq-seq).Msg("HLS segment already expired, the player fell too far behind live")
	default:
		event.Uint64("segments_ahead", seq-info.newestSeq).Msg("HLS segment does not exist yet")
	}
}

// serveBytes answers with an explicit Content-Length (no chunked encoding) and handles HEAD and Range requests, which some players issue even for live streams.
func serveBytes(w http.ResponseWriter, r *http.Request, data []byte) {
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Header.Get("Range") != "" {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data) // #nosec G705 -- our own playlist/MPEG-TS bytes, not user input
}
