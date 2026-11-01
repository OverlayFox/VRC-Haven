package dashboard

import "fmt"

type Config struct {
	Port    int
	Address string

	Username string
	Password string

	IsFlagship bool `koanf:"-"`
}

func (c Config) Validate() error {
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	return nil
}

func (c *Config) SetDefaults() {
	*c = Config{
		Port:    8080,
		Address: "127.0.0.1",
	}
}
