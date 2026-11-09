package src_test

import (
	"os"
	"strings"
	"testing"

	"github.com/OverlayFox/VRC-Haven/src"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
)

const validEnvFile = `# comments and blank lines are ignored

SRT_PASSPHRASE=passphrase-from-file
SRT_STREAM_ID=stream-from-file
HAVEN_FLAGSHIP_ADDRESS=flagship.example.com
HAVEN_FLAGSHIP_PORT=8890
UNKNOWN_VARIABLE=ignored
`

func writeEnvFile(t *testing.T, content string) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env", []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigReadsEnvFile(t *testing.T) {
	writeEnvFile(t, validEnvFile)

	config, err := src.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if config.SRT.Passphrase != "passphrase-from-file" {
		t.Errorf("passphrase = %q", config.SRT.Passphrase)
	}
	if config.SRT.StreamID != "stream-from-file" {
		t.Errorf("stream ID = %q", config.SRT.StreamID)
	}
	if config.Haven.FlagshipPort != 8890 {
		t.Errorf("flagship port = %d", config.Haven.FlagshipPort)
	}
	if config.HLS.Port != 8080 {
		t.Errorf("HLS port = %d, want the default when the file doesn't set it", config.HLS.Port)
	}
}

func TestLoadConfigEnvVarOverridesEnvFile(t *testing.T) {
	writeEnvFile(t, validEnvFile)
	t.Setenv("SRT_PASSPHRASE", "passphrase-from-env")

	config, err := src.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if config.SRT.Passphrase != "passphrase-from-env" {
		t.Errorf("passphrase = %q", config.SRT.Passphrase)
	}
	if config.SRT.StreamID != "stream-from-file" {
		t.Errorf("stream ID = %q", config.SRT.StreamID)
	}
}

func TestLoadConfigBlankValueKeepsDefault(t *testing.T) {
	writeEnvFile(t, validEnvFile+"HAVEN_FLAGSHIP_PORT=\nHLS_ADDRESS=\n")
	t.Setenv("HLS_PORT", "")

	config, err := src.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if config.Haven.FlagshipPort != 8890 {
		t.Errorf("flagship port = %d", config.Haven.FlagshipPort)
	}
	var hlsDefaults hls.Config
	hlsDefaults.SetDefaults()
	if config.HLS.Address != hlsDefaults.Address {
		t.Errorf("HLS address = %q, want default %q", config.HLS.Address, hlsDefaults.Address)
	}
	if config.HLS.Port != hlsDefaults.Port {
		t.Errorf("HLS port = %d, want default %d", config.HLS.Port, hlsDefaults.Port)
	}
}

func TestLoadConfigBlankRequiredValue(t *testing.T) {
	writeEnvFile(t, validEnvFile+"HAVEN_FLAGSHIP_ADDRESS=\n")

	_, err := src.LoadConfig(false)
	if err == nil || !strings.Contains(err.Error(), "HAVEN_FLAGSHIP_ADDRESS is required") {
		t.Fatalf("err = %v, want HAVEN_FLAGSHIP_ADDRESS is required", err)
	}
}

func TestLoadConfigWithoutEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SRT_PASSPHRASE", "passphrase-from-env")
	t.Setenv("SRT_STREAM_ID", "stream-from-env")
	t.Setenv("HAVEN_FLAGSHIP_ADDRESS", "flagship.example.com")
	t.Setenv("HAVEN_FLAGSHIP_PORT", "8890")

	config, err := src.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if config.SRT.Passphrase != "passphrase-from-env" {
		t.Errorf("passphrase = %q", config.SRT.Passphrase)
	}
}

func TestLoadConfigInvalidEnvFile(t *testing.T) {
	writeEnvFile(t, "not a valid line\n")

	_, err := src.LoadConfig(false)
	if err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("err = %v, want an error naming .env", err)
	}
}
