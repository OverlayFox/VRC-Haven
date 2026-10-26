package hls

import (
	"fmt"
	"time"
)

type Config struct {
	Port    int
	Address string

	IsFlagship bool `koanf:"-"`

	ReadTimeout    time.Duration
	ReadQueueSize  int
	WriteTimeout   time.Duration
	WriteQueueSize int
}

func (c *Config) Validate() error {
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}

	return nil
}

func (c *Config) SetDefaults() {
	*c = Config{
		Port:    8890,
		Address: "0.0.0.0",

		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,

		ReadQueueSize:  8192,
		WriteQueueSize: 8192,
	}
}
