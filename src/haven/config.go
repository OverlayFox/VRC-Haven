package haven

import (
	"errors"
	"fmt"
)

type Config struct {
	PublicAddress string `koanf:"public_address"`
	PublicPort    int    `koanf:"public_port"`

	FlagshipAddress string `koanf:"flagship_address" required:"escort"`
	FlagshipPort    int    `koanf:"flagship_port"`

	IsFlagship bool `koanf:"-"`
}

func (c *Config) Validate() error {
	if c.PublicPort < 0 || c.PublicPort > 65535 {
		return fmt.Errorf("invalid port: %d", c.PublicPort)
	}
	if !c.IsFlagship {
		if c.FlagshipAddress == "" {
			return errors.New("flagship address is required")
		}
		if c.FlagshipPort < 0 || c.FlagshipPort > 65535 {
			return fmt.Errorf("invalid port: %d", c.FlagshipPort)
		}
	}
	return nil
}

func (c *Config) SetDefaults() {
	*c = Config{
		FlagshipPort: 8890,
	}
}
