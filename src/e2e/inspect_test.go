//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// This file holds a deliberately small, strict reader for what the server sends players: the media playlist and MPEG-TS segments.
// It only inspects bytes, it never decodes media.

const (
	tsSyncByte = 0x47
	patPID     = 0x0000

	streamTypeH264 = 0x1B
	streamTypeAAC  = 0x0F // ADTS

	nalTypeIDR = 5
	nalTypeSPS = 7
	nalTypePPS = 8

	ptsClockHz = 90_000
)

type mediaPlaylist struct {
	version        int
	targetDuration int
	mediaSeq       uint64
	hasMediaSeq    bool
	independent    bool
	ended          bool
	discontinuity  bool
	segments       []playlistSegment
}

type playlistSegment struct {
	seq      uint64
	duration float64
	uri      string
}

// parsePlaylist reads a media playlist the way a strict player does: #EXTM3U first and every #EXTINF followed by its URI.
func parsePlaylist(body []byte) (*mediaPlaylist, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	if !sc.Scan() || sc.Text() != "#EXTM3U" {
		return nil, errors.New("playlist does not start with #EXTM3U")
	}

	p := &mediaPlaylist{}
	var pendingDuration *float64
	for sc.Scan() {
		line := sc.Text()
		if strings.HasSuffix(line, "\r") {
			return nil, errors.New("playlist uses CRLF line endings")
		}
		tag, value, _ := strings.Cut(line, ":")
		var err error
		switch {
		case line == "":
		case tag == "#EXT-X-VERSION":
			p.version, err = strconv.Atoi(value)
		case tag == "#EXT-X-TARGETDURATION":
			p.targetDuration, err = strconv.Atoi(value)
		case tag == "#EXT-X-MEDIA-SEQUENCE":
			p.mediaSeq, err = strconv.ParseUint(value, 10, 64)
			p.hasMediaSeq = true
		case line == "#EXT-X-INDEPENDENT-SEGMENTS":
			p.independent = true
		case line == "#EXT-X-ENDLIST":
			p.ended = true
		case line == "#EXT-X-DISCONTINUITY":
			p.discontinuity = true
		case tag == "#EXTINF":
			durStr, _, _ := strings.Cut(value, ",")
			var d float64
			d, err = strconv.ParseFloat(durStr, 64)
			pendingDuration = &d
		case strings.HasPrefix(line, "#"):
			// Other tags are allowed, players ignore what they do not know.
		default:
			if pendingDuration == nil {
				return nil, fmt.Errorf("segment URI %q without #EXTINF", line)
			}
			p.segments = append(p.segments, playlistSegment{
				seq:      p.mediaSeq + uint64(len(p.segments)),
				duration: *pendingDuration,
				uri:      line,
			})
			pendingDuration = nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", line, err)
		}
	}
	if pendingDuration != nil {
		return nil, errors.New("playlist ends with #EXTINF but no URI")
	}
	return p, sc.Err()
}

type pes struct {
	pts, dts     int64 // 90 kHz, dts equals pts when the PES has no DTS
	data         []byte
	randomAccess bool // random access indicator of the PES's first packet
}

type tsSegment struct {
	firstPIDs   []uint16 // PIDs of the first packets, to check PAT and PMT lead the segment
	pmtPID      uint16
	streamTypes map[uint16]byte // elementary PID -> stream type from the PMT
	videoPID    uint16
	audioPID    uint16
	video       []pes
	audio       []pes
	pcrCount    int
}

// ccTracker checks continuity counters per PID, across segments as players read them back to back.
type ccTracker map[uint16]byte

// tsPacket is the header of one TS packet and its payload.
type tsPacket struct {
	pid           uint16
	pusi          bool
	hasPayload    bool
	counter       byte
	discontinuity bool
	randomAccess  bool
	hasPCR        bool
	payload       []byte
}

func readPacket(pkt []byte) (tsPacket, error) {
	if pkt[0] != tsSyncByte {
		return tsPacket{}, fmt.Errorf("sync byte 0x%02X instead of 0x47", pkt[0])
	}
	afc := (pkt[3] >> 4) & 0x03
	p := tsPacket{
		pid:        uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2]),
		pusi:       pkt[1]&0x40 != 0,
		hasPayload: afc&0x01 != 0,
		counter:    pkt[3] & 0x0F,
		payload:    pkt[4:],
	}
	if afc&0x02 != 0 {
		afLen := int(pkt[4])
		if afLen > 183 {
			return tsPacket{}, fmt.Errorf("adaptation field length %d", afLen)
		}
		if afLen > 0 {
			p.discontinuity = pkt[5]&0x80 != 0
			p.randomAccess = pkt[5]&0x40 != 0
		}
		_, p.hasPCR = packetPCR(pkt)
		p.payload = pkt[5+afLen:]
	}
	return p, nil
}

// tsParser collects one segment's tables and PES packets.
type tsParser struct {
	seg     *tsSegment
	cc      ccTracker
	pending map[uint16]*pes
}

// parseTS reads one segment. Continuity counters are checked against cc, so it can span consecutive segments.
func parseTS(data []byte, cc ccTracker) (*tsSegment, error) {
	if len(data) == 0 || len(data)%tsPacketSize != 0 {
		return nil, fmt.Errorf("segment is %d bytes, not a whole number of %d byte TS packets", len(data), tsPacketSize)
	}

	p := &tsParser{
		seg:     &tsSegment{streamTypes: map[uint16]byte{}},
		cc:      cc,
		pending: map[uint16]*pes{},
	}
	for i := 0; i < len(data); i += tsPacketSize {
		pkt, err := readPacket(data[i : i+tsPacketSize])
		if err == nil {
			err = p.packet(pkt)
		}
		if err != nil {
			return nil, fmt.Errorf("packet %d: %w", i/tsPacketSize, err)
		}
	}
	for _, pid := range []uint16{p.seg.videoPID, p.seg.audioPID} {
		if err := p.finish(pid); err != nil {
			return nil, err
		}
	}
	return p.seg, nil
}

func (p *tsParser) packet(pkt tsPacket) error {
	seg := p.seg
	if len(seg.firstPIDs) < 2 {
		seg.firstPIDs = append(seg.firstPIDs, pkt.pid)
	}
	if pkt.hasPCR {
		seg.pcrCount++
	}
	if !pkt.hasPayload {
		return nil // the continuity counter only advances with a payload
	}

	if last, seen := p.cc[pkt.pid]; seen && !pkt.discontinuity && pkt.counter != (last+1)&0x0F {
		return fmt.Errorf("PID 0x%X continuity counter jumped from %d to %d", pkt.pid, last, pkt.counter)
	}
	p.cc[pkt.pid] = pkt.counter

	switch {
	case pkt.pid == patPID:
		if pkt.pusi {
			pmtPID, err := parsePAT(pkt.payload)
			if err != nil {
				return err
			}
			seg.pmtPID = pmtPID
		}
	case seg.pmtPID != 0 && pkt.pid == seg.pmtPID:
		if pkt.pusi {
			return seg.parsePMT(pkt.payload)
		}
	case pkt.pid == seg.videoPID || pkt.pid == seg.audioPID:
		if pkt.pusi {
			if err := p.finish(pkt.pid); err != nil {
				return err
			}
			p.pending[pkt.pid] = &pes{randomAccess: pkt.randomAccess}
		}
		if pending := p.pending[pkt.pid]; pending != nil {
			pending.data = append(pending.data, pkt.payload...)
		}
	case seg.pmtPID == 0:
		return fmt.Errorf("PID 0x%X before the PAT and PMT", pkt.pid)
	}
	return nil
}

// finish parses the PES collected for pid and adds it to the segment.
func (p *tsParser) finish(pid uint16) error {
	pending := p.pending[pid]
	delete(p.pending, pid)
	if pending == nil {
		return nil
	}
	if err := parsePESHeader(pending); err != nil {
		return fmt.Errorf("PID 0x%X: %w", pid, err)
	}
	switch pid {
	case p.seg.videoPID:
		p.seg.video = append(p.seg.video, *pending)
	case p.seg.audioPID:
		p.seg.audio = append(p.seg.audio, *pending)
	}
	return nil
}

// psiSection returns the section after the pointer field, checking the table ID and cutting it to its length without the CRC.
func psiSection(payload []byte, tableID byte) ([]byte, error) {
	if len(payload) < 1 {
		return nil, errors.New("empty PSI payload")
	}
	start := 1 + int(payload[0])
	if len(payload) < start+3 {
		return nil, errors.New("PSI section truncated")
	}
	section := payload[start:]
	if section[0] != tableID {
		return nil, fmt.Errorf("table ID 0x%02X instead of 0x%02X", section[0], tableID)
	}
	length := int(section[1]&0x0F)<<8 | int(section[2])
	if len(section) < 3+length || length < 9 {
		return nil, fmt.Errorf("table 0x%02X does not fit in one packet", tableID)
	}
	return section[:3+length-4], nil
}

func parsePAT(payload []byte) (uint16, error) {
	section, err := psiSection(payload, 0x00)
	if err != nil {
		return 0, fmt.Errorf("PAT: %w", err)
	}
	for off := 8; off+4 <= len(section); off += 4 {
		program := uint16(section[off])<<8 | uint16(section[off+1])
		if program != 0 {
			return uint16(section[off+2]&0x1F)<<8 | uint16(section[off+3]), nil
		}
	}
	return 0, errors.New("PAT lists no program")
}

func (seg *tsSegment) parsePMT(payload []byte) error {
	section, err := psiSection(payload, 0x02)
	if err != nil {
		return fmt.Errorf("PMT: %w", err)
	}
	if len(section) < 12 {
		return errors.New("PMT truncated")
	}
	infoLen := int(section[10]&0x0F)<<8 | int(section[11])
	for off := 12 + infoLen; off+5 <= len(section); {
		streamType := section[off]
		pid := uint16(section[off+1]&0x1F)<<8 | uint16(section[off+2])
		esInfoLen := int(section[off+3]&0x0F)<<8 | int(section[off+4])
		seg.streamTypes[pid] = streamType
		switch streamType {
		case streamTypeH264:
			seg.videoPID = pid
		case streamTypeAAC:
			seg.audioPID = pid
		}
		off += 5 + esInfoLen
	}
	return nil
}

func parsePESHeader(p *pes) error {
	d := p.data
	if len(d) < 9 || d[0] != 0 || d[1] != 0 || d[2] != 1 {
		return errors.New("PES does not start with a start code")
	}
	flags := d[7] >> 6
	headerLen := int(d[8])
	if len(d) < 9+headerLen {
		return errors.New("PES header truncated")
	}
	if flags&0x02 == 0 {
		return errors.New("PES has no PTS")
	}
	p.pts = parseTimestamp(d[9:14])
	p.dts = p.pts
	if flags&0x01 != 0 {
		p.dts = parseTimestamp(d[14:19])
	}
	p.data = d[9+headerLen:]
	return nil
}

func parseTimestamp(b []byte) int64 {
	return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

// nalTypes lists the H.264 NAL unit types of an Annex B access unit in order.
func nalTypes(au []byte) []byte {
	var types []byte
	for i := 0; i+3 < len(au); i++ {
		if au[i] == 0 && au[i+1] == 0 && au[i+2] == 1 {
			types = append(types, au[i+3]&0x1F)
			i += 3
		}
	}
	return types
}
