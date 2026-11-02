package dashboard

import (
	"errors"
	"fmt"
)

type Config struct {
	Port    int
	Address string

	Username string
	Password string

	IsFlagship bool `koanf:"-"`
}

func (c Config) Validate() error {
	if c.Username == "" && c.Password == "" {
		return nil // Dashboard is disabled
	}
	if c.Username == "" && c.Password != "" {
		return errors.New("username is required when password is set")
	}
	if c.Username != "" && c.Password == "" {
		return errors.New("password is required when username is set")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	return nil
}

func (c *Config) SetDefaults() {
	*c = Config{
		Port:    8090,
		Address: "127.0.0.1",
	}
}
