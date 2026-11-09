//go:build e2e

package e2e_test

import (
	"bytes"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	// followDuration is how long the test follows the live playlist, long enough for the live window to slide.
	followDuration = 14 * time.Second
	pollInterval   = 2 * time.Second

	// extinfTolerance is how far EXTINF may differ from the segment's actual timestamps.
	extinfTolerance = 0.1
)

// TestHLSConformance follows the live stream like VRChat's AVPro player on PC does, which reads HLS through Windows Media Foundation,
// and checks what Media Foundation is strict about. The Windows job additionally plays the stream through Media Foundation itself.
func TestHLSConformance(t *testing.T) {
	s := startServer(t)
	pub := publish(t, s)
	t.Cleanup(func() {
		if pub.err != nil {
			t.Errorf("publisher: %v", pub.err)
		}
	})

	resp, body := waitForPlaylist(t, s)
	checkPlaylistResponse(t, resp)
	first, err := parsePlaylist(body)
	if err != nil {
		t.Fatalf("parse playlist: %v\n%s", err, body)
	}
	checkPlaylist(t, first, body)
	if first.mediaSeq != 0 {
		t.Errorf("first served playlist has EXT-X-MEDIA-SEQUENCE %d, Media Foundation needs the stream to start at 0", first.mediaSeq)
	}

	segments := map[uint64][]byte{}
	durations := map[uint64]float64{}
	fetch := func(p *mediaPlaylist) {
		for _, seg := range p.segments {
			if _, ok := segments[seg.seq]; ok {
				if durations[seg.seq] != seg.duration {
					t.Errorf("segment %d changed its EXTINF from %.3f to %.3f between playlist refreshes", seg.seq, durations[seg.seq], seg.duration)
				}
				continue
			}
			segResp, data := httpGet(t, http.MethodGet, s.hlsBase+seg.uri, nil)
			if segResp.StatusCode != http.StatusOK {
				t.Fatalf("GET segment %s: status %d", seg.uri, segResp.StatusCode)
			}
			checkSegmentResponse(t, segResp, len(data))
			segments[seg.seq] = data
			durations[seg.seq] = seg.duration
		}
	}
	fetch(first)
	checkByteServing(t, s, first.segments[0].uri, segments[first.segments[0].seq])

	last := first
	for deadline := time.Now().Add(followDuration); time.Now().Before(deadline); {
		time.Sleep(pollInterval)
		resp, body := httpGet(t, http.MethodGet, s.playlistURL(), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("playlist refresh: status %d: %s", resp.StatusCode, body)
		}
		p, err := parsePlaylist(body)
		if err != nil {
			t.Fatalf("parse refreshed playlist: %v\n%s", err, body)
		}
		checkPlaylist(t, p, body)
		if p.mediaSeq < last.mediaSeq {
			t.Fatalf("EXT-X-MEDIA-SEQUENCE went backwards from %d to %d", last.mediaSeq, p.mediaSeq)
		}
		fetch(p)
		last = p
	}
	t.Logf("followed the playlist from EXT-X-MEDIA-SEQUENCE %d to %d", first.mediaSeq, last.mediaSeq)
	if last.mediaSeq == first.mediaSeq {
		t.Errorf("EXT-X-MEDIA-SEQUENCE stayed at %d for %s, the live window never slid", last.mediaSeq, followDuration)
	}

	checkSegments(t, segments, durations)
	checkPreflight(t, s)
}

func checkPlaylistResponse(t *testing.T, resp *http.Response) {
	t.Helper()

	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("playlist Content-Type is %q, want application/vnd.apple.mpegurl", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("playlist Cache-Control is %q, a cached live playlist stalls players", cc)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Error("playlist has no Access-Control-Allow-Origin header")
	}
}

func checkPlaylist(t *testing.T, p *mediaPlaylist, body []byte) {
	t.Helper()

	if p.version != 3 {
		t.Errorf("EXT-X-VERSION is %d, want 3\n%s", p.version, body)
	}
	if !p.hasMediaSeq {
		t.Errorf("playlist has no EXT-X-MEDIA-SEQUENCE\n%s", body)
	}
	if !p.independent {
		t.Errorf("playlist has no EXT-X-INDEPENDENT-SEGMENTS\n%s", body)
	}
	if p.ended {
		t.Errorf("playlist has EXT-X-ENDLIST while the publisher is live\n%s", body)
	}
	if p.discontinuity {
		t.Errorf("playlist has EXT-X-DISCONTINUITY although the publisher never reconnected\n%s", body)
	}
	if len(p.segments) < 3 {
		t.Errorf("playlist lists %d segments, players need at least 3 to start a live stream\n%s", len(p.segments), body)
	}
	for _, seg := range p.segments {
		// RFC 8216 4.3.3.1: each EXTINF rounded to the nearest integer must not exceed the target duration
		if int(math.Round(seg.duration)) > p.targetDuration {
			t.Errorf("segment %s lasts %.3fs, longer than EXT-X-TARGETDURATION %d", seg.uri, seg.duration, p.targetDuration)
		}
		if seg.duration <= 0 {
			t.Errorf("segment %s has EXTINF %.3f", seg.uri, seg.duration)
		}
		if strings.Contains(seg.uri, "://") || strings.HasPrefix(seg.uri, "/") || !strings.HasSuffix(seg.uri, ".ts") {
			t.Errorf("segment URI %q is not a relative .ts path", seg.uri)
		}
	}
}

func checkSegmentResponse(t *testing.T, resp *http.Response, size int) {
	t.Helper()

	if ct := resp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("segment Content-Type is %q, want video/mp2t", ct)
	}
	if resp.ContentLength != int64(size) {
		t.Errorf("segment Content-Length is %d, body is %d bytes", resp.ContentLength, size)
	}
}

// checkByteServing covers HEAD and Range requests, which players issue even for live segments.
func checkByteServing(t *testing.T, s *testServer, uri string, data []byte) {
	t.Helper()

	head, body := httpGet(t, http.MethodHead, s.hlsBase+uri, nil)
	if head.StatusCode != http.StatusOK || len(body) != 0 {
		t.Errorf("HEAD segment: status %d with %d body bytes", head.StatusCode, len(body))
	}
	if head.ContentLength != int64(len(data)) {
		t.Errorf("HEAD segment Content-Length is %d, segment is %d bytes", head.ContentLength, len(data))
	}

	ranged, part := httpGet(t, http.MethodGet, s.hlsBase+uri, http.Header{"Range": {"bytes=0-187"}})
	if ranged.StatusCode != http.StatusPartialContent {
		t.Errorf("ranged GET segment: status %d, want 206", ranged.StatusCode)
	} else if !bytes.Equal(part, data[:tsPacketSize]) {
		t.Errorf("ranged GET segment returned %d bytes that differ from the segment's first packet", len(part))
	}
}

func checkPreflight(t *testing.T, s *testServer) {
	t.Helper()

	resp, _ := httpGet(t, http.MethodOptions, s.playlistURL(), http.Header{
		"Origin":                        {"https://example.invalid"},
		"Access-Control-Request-Method": {"GET"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("CORS preflight: status %d, want 204", resp.StatusCode)
	}
}

// checkSegments parses the segments in sequence order, as a player reads them, and checks each and the transition between neighbours.
func checkSegments(t *testing.T, segments map[uint64][]byte, durations map[uint64]float64) {
	t.Helper()

	seqs := make([]uint64, 0, len(segments))
	for seq := range segments {
		seqs = append(seqs, seq)
	}
	slices.Sort(seqs)
	if len(seqs) < 6 {
		t.Fatalf("only %d segments were published while following the stream", len(seqs))
	}

	cc := ccTracker{}
	var prev *tsSegment
	var prevSeq uint64
	for _, seq := range seqs {
		if prev != nil && seq != prevSeq+1 {
			// A player that fell behind skips segments, continuity restarts at the next one it reads
			cc = ccTracker{}
			prev = nil
		}
		seg, err := parseTS(segments[seq], cc)
		if err != nil {
			t.Errorf("segment %d: %v", seq, err)
			cc, prev = ccTracker{}, nil
			continue
		}
		checkSegment(t, seq, seg)

		if prev != nil && len(prev.video) > 0 && len(seg.video) > 0 {
			lastDTS := prev.video[len(prev.video)-1].dts
			if seg.video[0].dts <= lastDTS {
				t.Errorf("segment %d starts at video DTS %d, not after segment %d's last DTS %d", seq, seg.video[0].dts, prevSeq, lastDTS)
			}
			actual := float64(seg.video[0].pts-prev.video[0].pts) / ptsClockHz
			if math.Abs(actual-durations[prevSeq]) > extinfTolerance {
				t.Errorf("segment %d has EXTINF %.3f but its video spans %.3fs", prevSeq, durations[prevSeq], actual)
			}
		}
		prev, prevSeq = seg, seq
	}
	t.Logf("checked %d segments, sequence %d to %d", len(seqs), seqs[0], seqs[len(seqs)-1])
}

func checkSegment(t *testing.T, seq uint64, seg *tsSegment) {
	t.Helper()

	// RFC 8216 3.2: a segment should start with a PAT and PMT, a player joining at this segment needs them before any media
	if len(seg.firstPIDs) < 2 || seg.firstPIDs[0] != patPID || seg.firstPIDs[1] != seg.pmtPID {
		t.Errorf("segment %d starts with PIDs %v instead of the PAT then the PMT (0x%X)", seq, seg.firstPIDs, seg.pmtPID)
	}
	if seg.videoPID == 0 || seg.audioPID == 0 {
		t.Fatalf("segment %d: PMT lists stream types %v, want H.264 (0x1B) and ADTS AAC (0x0F)", seq, seg.streamTypes)
	}
	if len(seg.video) == 0 {
		t.Fatalf("segment %d has no video", seq)
	}
	// The muxer's comment on writeAudioAccessUnit explains why Media Foundation needs audio in every segment
	if len(seg.audio) == 0 {
		t.Errorf("segment %d has no audio, Media Foundation's audio clock would never start", seq)
	}
	if seg.pcrCount == 0 {
		t.Errorf("segment %d carries no PCR", seq)
	}

	first := seg.video[0]
	if !first.randomAccess {
		t.Errorf("segment %d's first video packet lacks the random access indicator", seq)
	}
	nals := nalTypes(first.data)
	sps, pps, idr := slices.Index(nals, nalTypeSPS), slices.Index(nals, nalTypePPS), slices.Index(nals, nalTypeIDR)
	if sps < 0 || pps < 0 || idr < 0 || sps > idr || pps > idr {
		t.Errorf("segment %d does not start with SPS and PPS before an IDR, its first access unit has NAL types %v", seq, nals)
	}

	for i := 1; i < len(seg.video); i++ {
		if seg.video[i].dts <= seg.video[i-1].dts {
			t.Errorf("segment %d: video DTS %d after %d does not increase", seq, seg.video[i].dts, seg.video[i-1].dts)
			break
		}
	}
	for i := 1; i < len(seg.audio); i++ {
		if seg.audio[i].pts <= seg.audio[i-1].pts {
			t.Errorf("segment %d: audio PTS %d after %d does not increase", seq, seg.audio[i].pts, seg.audio[i-1].pts)
			break
		}
	}
	for _, a := range seg.audio {
		if !a.randomAccess {
			t.Errorf("segment %d: audio PES at PTS %d lacks the random access indicator, Media Foundation never starts audio without it", seq, a.pts)
			break
		}
	}
	for _, v := range seg.video {
		if v.pts < v.dts {
			t.Errorf("segment %d: video PTS %d before its DTS %d", seq, v.pts, v.dts)
			break
		}
	}
}
