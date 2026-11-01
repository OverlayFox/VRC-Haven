package hls

import (
	"fmt"
	"time"
)

type Config struct {
	Port    int
	Address string

	IsFlagship bool `koanf:"-"`

	ReadTimeout    time.Duration `koanf:"read_timeout"`
	ReadQueueSize  int           `koanf:"read_queue_size"`
	WriteTimeout   time.Duration `koanf:"write_timeout"`
	WriteQueueSize int           `koanf:"write_queue_size"`
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
		Address: "127.0.0.1",

		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,

		ReadQueueSize:  8192,
		WriteQueueSize: 8192,
	}
}
