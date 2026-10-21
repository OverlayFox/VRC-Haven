package srt_test

import (
	"net"
	"strings"
	"testing"

	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
	"github.com/OverlayFox/VRC-Haven/src/types"
)

func TestResolveAdvertisedAddr(t *testing.T) {
	remote := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40123}

	tests := []struct {
		advertised string
		want       string
		wantErr    bool
	}{
		{advertised: ":8555", want: "203.0.113.7:8555"},
		{advertised: "198.51.100.1:8555", want: "198.51.100.1:8555"},
		{advertised: "escort.example.com:80", want: "escort.example.com:80"},
		{advertised: "[2001:db8::1]:8555", want: "[2001:db8::1]:8555"},
		{advertised: "", wantErr: true},
		{advertised: "8555", wantErr: true},
		{advertised: ":0", wantErr: true},
		{advertised: ":http", wantErr: true},
		{advertised: "evil.com/path:80", wantErr: true},
		{advertised: "user@evil.com:80", wantErr: true},
		{advertised: "-bad.example.com:80", wantErr: true},
	}
	for _, tt := range tests {
		got, err := srt.ResolveAdvertisedAddr(tt.advertised, remote)
		if (err != nil) != tt.wantErr {
			t.Errorf("resolveAdvertisedAddr(%q) error = %v, wantErr %v", tt.advertised, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveAdvertisedAddr(%q) = %q, want %q", tt.advertised, got, tt.want)
		}
	}
}

func TestEscortStreamIDRoundTrip(t *testing.T) {
	for _, addr := range []string{":8555", "escort.example.com:80", "[2001:db8::1]:8555"} {
		parts := strings.SplitN(srt.EscortStreamID("test", addr), ":", 3)
		if len(parts) != 3 {
			t.Fatalf("EscortStreamID(%q) has %d parts", addr, len(parts))
		}
		if types.ConnectionTypeFromString(parts[0]) != types.ConnectionTypeEscort || parts[1] != "test" || parts[2] != addr {
			t.Errorf("EscortStreamID(%q) parsed to %q", addr, parts)
		}
	}
}
