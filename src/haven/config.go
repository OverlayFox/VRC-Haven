package haven

import "fmt"

type Config struct {
	PublicAddress string `konf:"public_address"`
	PublicPort    int    `konf:"public_port"`

	FlagshipAddress string `konf:"flagship_address"`
	FlagshipPort    int    `konf:"flagship_port"`

	IsFlagship bool `konf:"-"`
}

func (c *Config) Validate() error {
	if !c.IsFlagship {
		if c.FlagshipPort < 0 || c.FlagshipPort > 65535 {
			return fmt.Errorf("invalid port: %d", c.FlagshipPort)
		}
	}
	return nil
}

func (c *Config) SetDefaults() {
	*c = Config{
		PublicPort: 8080,
	}
}
