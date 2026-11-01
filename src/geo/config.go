package geo

import (
	"errors"
	"fmt"
	"os"
)

const defaultDBDir = "./geoip2Files"

type Config struct {
	LicenseKey string
	AccountID  string
	Dir        string `koanf:"db_directory"`
}

func (c *Config) Validate() error {
	if c.LicenseKey == "" {
		return errors.New("license key is required")
	}
	if c.AccountID == "" {
		return errors.New("account ID is required")
	}
	if c.Dir == "" {
		return errors.New("directory for geo database is required")
	}
	return checkDirAccess(c.Dir)
}

func checkDirAccess(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create directory %q: %w", dir, err)
	}
	if _, err := os.ReadDir(dir); err != nil {
		return fmt.Errorf("read directory %q: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".access-check-*")
	if err != nil {
		return fmt.Errorf("write to directory %q: %w", dir, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close file in directory %q: %w", dir, err)
	}
	if err := os.Remove(f.Name()); err != nil {
		return fmt.Errorf("delete from directory %q: %w", dir, err)
	}
	return nil
}

func (c *Config) SetDefaults() {
	if c.Dir == "" {
		c.Dir = defaultDBDir
	}
}
