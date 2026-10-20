package hls

import (
	"bytes"
	"fmt"
	"math"
	"time"
)

const (
	playlistFilename = "index.m3u8"

	// playlistWindowTargetDurations is the minimum listed duration in target durations.
	playlistWindowTargetDurations = 6
	playlistMaxSegments           = 20

	// playlistMinSegments is how many segments must exist before the playlist is served.
	// With fewer, players cannot hold back 3 segments and stall right after starting.
	playlistMinSegments = 3
)

type segment struct {
	seq      uint64
	duration time.Duration
	data     []byte
}

type livePlaylist struct {
	segments       []segment
	targetDuration int
	rendered       []byte
}

func newLivePlaylist() *livePlaylist {
	return &livePlaylist{}
}

func (p *livePlaylist) addSegment(seg segment) {
	p.segments = append(p.segments, seg)

	if td := max(1, int(math.Round(seg.duration.Seconds()))); td > p.targetDuration {
		p.targetDuration = td
	}

	listed := p.listedCount()
	if excess := len(p.segments) - 2*listed; excess > 0 {
		clear(p.segments[:excess])
		p.segments = p.segments[excess:]
	}

	p.rendered = p.render(p.segments[len(p.segments)-listed:])
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

func (p *livePlaylist) render(listed []segment) []byte {
	var b bytes.Buffer
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n") // lowest version allowing decimal EXTINF durations
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", p.targetDuration)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", listed[0].seq)
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, seg := range listed {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", seg.duration.Seconds(), segmentFilename(seg.seq))
	}
	return b.Bytes()
}

func (p *livePlaylist) ready() bool {
	return len(p.segments) >= playlistMinSegments
}

// snapshot returns the rendered playlist. The slice is never mutated afterwards,
// so it may be written to clients after releasing the connection lock.
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
