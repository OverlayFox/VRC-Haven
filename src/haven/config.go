package haven

import "fmt"

type Config struct {
	PublicAddress string `koanf:"public_address"`
	PublicPort    int    `koanf:"public_port"`

	FlagshipAddress string `koanf:"flagship_address"`
	FlagshipPort    int    `koanf:"flagship_port"`

	IsFlagship bool `koanf:"-"`
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
