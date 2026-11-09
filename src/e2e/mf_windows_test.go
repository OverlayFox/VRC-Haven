//go:build e2e && windows

package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	// mfRequiredPlayback is how much playback progress counts as playing, several segments past startup.
	mfRequiredPlayback = "10"
	// mfPlayerTimeout leaves the player time to start inside the 60s test clip, see testdata/e2e/README.md.
	mfPlayerTimeout = "40"
)

// TestMediaFoundationPlayback plays the live stream through Windows Media Foundation, the stack VRChat's AVPro player uses on PC.
// MFPLAYER points at the built test/mfplayer executable.
func TestMediaFoundationPlayback(t *testing.T) {
	player := os.Getenv("MFPLAYER")
	if player == "" {
		t.Skip("MFPLAYER is not set, build test/mfplayer and point MFPLAYER at its executable")
	}

	s := startServer(t)
	pub := publish(t, s)
	t.Cleanup(func() {
		if pub.err != nil {
			t.Errorf("publisher: %v", pub.err)
		}
	})
	// Press play once the stream is live, like a viewer in VRChat would
	waitForPlaylist(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, player, s.playlistURL(), mfRequiredPlayback, mfPlayerTimeout).CombinedOutput()
	t.Logf("mfplayer output:\n%s", out)
	if err != nil {
		t.Fatalf("Media Foundation did not play the stream: %v", err)
	}
}
