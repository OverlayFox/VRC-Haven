package srt

import (
	"errors"
	"fmt"
	"strings"
)

type Config struct {
	Port    int
	Address string

	Passphrase string
	StreamID   string `koanf:"stream_id"`

	IsFlagship bool `koanf:"-"`
}

func (c *Config) Validate() error {
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	if strings.TrimSpace(c.StreamID) == "" {
		return errors.New("srt.StreamID is empty")
	}
	if strings.Contains(c.StreamID, ":") {
		return fmt.Errorf("srt.streamID '%q' contains `:` which is a forbidden character", c.StreamID)
	}

	if strings.TrimSpace(c.Passphrase) == "" {
		return errors.New("srt.Passphrase is empty")
	}
	if n := len(c.Passphrase); n < 10 || n > 79 {
		return fmt.Errorf("srt.Passphrase must be 10 to 79 characters, go '%d'", n)
	}
	return nil
}

func (c *Config) SetDefaults() {
	*c = Config{
		Port:    8890,
		Address: "127.0.0.1",
	}
}
