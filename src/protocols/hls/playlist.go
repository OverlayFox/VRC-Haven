package hls

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	playlistFilename = "index.m3u8"

	playlistWindowTargetDurations = 6
	playlistMaxSegments           = 20

	playlistMinSegments = 3
)

type segment struct {
	seq      uint64
	duration time.Duration
	data     []byte

	discontinuity bool
}

type playlistInfo struct {
	mediaSeq       uint64
	newestSeq      uint64
	oldestSeq      uint64
	listed         int
	targetDuration int
}

type livePlaylist struct {
	session        string
	segments       []segment
	targetDuration int
	rendered       []byte
	listed         int

	nextSeq                uint64
	pendingDiscontinuity   bool
	droppedDiscontinuities uint64
	ended                  bool
}

// newLivePlaylist starts EXT-X-MEDIA-SEQUENCE at 0. Media Foundation (VRChat on PC) accepts the stream but never plays it with a large sequence such as Unix time.
func newLivePlaylist() *livePlaylist {
	return &livePlaylist{
		session: strconv.FormatInt(time.Now().UnixMilli(), 36),
	}
}

func (p *livePlaylist) filename(seq uint64) string {
	return fmt.Sprintf("segment_%s_%d.ts", p.session, seq)
}

// parseFilename returns the sequence of a segment of this session.
func (p *livePlaylist) parseFilename(filename string) (uint64, bool) {
	seqStr, ok := strings.CutPrefix(filename, "segment_"+p.session+"_")
	if !ok {
		return 0, false
	}
	seqStr, ok = strings.CutSuffix(seqStr, ".ts")
	if !ok {
		return 0, false
	}
	seq, err := strconv.ParseUint(seqStr, 10, 64)
	return seq, err == nil
}

// addSegment appends a segment and returns the sequence it was given.
func (p *livePlaylist) addSegment(duration time.Duration, data []byte) uint64 {
	seg := segment{
		seq:           p.nextSeq,
		duration:      duration,
		data:          data,
		discontinuity: p.pendingDiscontinuity,
	}
	p.nextSeq++
	p.pendingDiscontinuity = false
	p.segments = append(p.segments, seg)

	if td := max(1, int(math.Round(seg.duration.Seconds()))); td > p.targetDuration {
		p.targetDuration = td
	}

	p.listed = p.listedCount()
	if excess := len(p.segments) - 2*p.listed; excess > 0 {
		p.droppedDiscontinuities += countDiscontinuities(p.segments[:excess])
		clear(p.segments[:excess])
		p.segments = p.segments[excess:]
	}

	p.render()
	return seg.seq
}

// markDiscontinuity flags the next segment as the start of a new publisher's media.
func (p *livePlaylist) markDiscontinuity() bool {
	if len(p.segments) == 0 {
		return false
	}
	p.pendingDiscontinuity = true
	return true
}

// end appends EXT-X-ENDLIST, after which players play out what is listed and report the stream as ended. The playlist must not change afterwards.
func (p *livePlaylist) end() {
	p.ended = true
	if len(p.segments) > 0 {
		p.render()
	}
}

func (p *livePlaylist) info() (playlistInfo, bool) {
	if len(p.segments) == 0 {
		return playlistInfo{}, false
	}
	return playlistInfo{
		mediaSeq:       p.segments[len(p.segments)-p.listed].seq,
		newestSeq:      p.segments[len(p.segments)-1].seq,
		oldestSeq:      p.segments[0].seq,
		listed:         p.listed,
		targetDuration: p.targetDuration,
	}, true
}

// listedCount returns how many of the newest segments make up the live window.
func (p *livePlaylist) listedCount() int {
	minDuration := time.Duration(playlistWindowTargetDurations*p.targetDuration) * time.Second

	var total time.Duration
	count := 0
	for i := len(p.segments) - 1; i >= 0 && count < playlistMaxSegments; i-- {
		total += p.segments[i].duration
		count++
		if total >= minDuration {
			break
		}
	}
	return count
}

func (p *livePlaylist) render() {
	unlisted := p.segments[:len(p.segments)-p.listed]
	listed := p.segments[len(p.segments)-p.listed:]

	var b bytes.Buffer
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n") // lowest version allowing decimal EXTINF durations
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", p.targetDuration)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", listed[0].seq)
	// Omitted until a discontinuity slid out of the window, so a playlist without reconnects stays as minimal as before
	if discontinuitySeq := p.droppedDiscontinuities + countDiscontinuities(unlisted); discontinuitySeq > 0 {
		fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", discontinuitySeq)
	}
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, seg := range listed {
		if seg.discontinuity {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", seg.duration.Seconds(), p.filename(seg.seq))
	}
	if p.ended {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	p.rendered = b.Bytes()
}

func (p *livePlaylist) ready() bool {
	return len(p.segments) >= playlistMinSegments
}

// snapshot returns the rendered playlist. The slice is never mutated afterwards,
// so it may be written to clients after releasing the stream lock.
func (p *livePlaylist) snapshot() []byte {
	return p.rendered
}

// segment returns a retained segment's data. The slice is immutable.
func (p *livePlaylist) segment(seq uint64) ([]byte, bool) {
	if len(p.segments) == 0 || seq < p.segments[0].seq {
		return nil, false
	}
	idx := seq - p.segments[0].seq
	if idx >= uint64(len(p.segments)) {
		return nil, false
	}
	return p.segments[idx].data, true
}

func countDiscontinuities(segments []segment) uint64 {
	var n uint64
	for _, seg := range segments {
		if seg.discontinuity {
			n++
		}
	}
	return n
}
