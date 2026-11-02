package logging

import "github.com/rs/zerolog"

type Config struct {
	Level zerolog.Level
}

func (c *Config) SetDefaults() {
	*c = Config{
		Level: zerolog.InfoLevel,
	}
}
