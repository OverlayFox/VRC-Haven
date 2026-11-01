package src

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"github.com/OverlayFox/VRC-Haven/src/dashboard"
	"github.com/OverlayFox/VRC-Haven/src/geo"
	"github.com/OverlayFox/VRC-Haven/src/haven"
	"github.com/OverlayFox/VRC-Haven/src/logging"
	"github.com/OverlayFox/VRC-Haven/src/profiling"
	"github.com/OverlayFox/VRC-Haven/src/protocols/hls"
	"github.com/OverlayFox/VRC-Haven/src/protocols/srt"
)

// Config is the main point where all configs life
//
// It's handled by github.com/knadh/koanf/v2 which will turn fields into values structs like `SRT.Port`
// ENV vars are converted to `SRT_PORT`.
type Config struct {
	Log      logging.Config
	Profiler profiling.Config

	SRT       srt.Config
	HLS       hls.Config
	Dashboard dashboard.Config
	Haven     haven.Config
	Geo       geo.Config
}

func DefaultConfig() Config {
	var c Config
	eachField(&c, func(f any) {
		if d, ok := f.(defaulter); ok {
			d.SetDefaults()
		}
	})
	return c
}

func (c *Config) Validate() error {
	var errs []error
	eachField(c, func(f any) {
		if v, ok := f.(validator); ok {
			errs = append(errs, v.Validate())
		}
	})
	return errors.Join(errs...)
}

func LoadConfig(isFlagship bool) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(structs.Provider(DefaultConfig(), "koanf"), nil); err != nil {
		return nil, err
	}

	names, err := envNames(k)
	if err != nil {
		return nil, err
	}
	err = k.Load(env.Provider(".", env.Opt{
		TransformFunc: func(name, value string) (string, any) { return names[name], value },
	}), nil)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := k.Unmarshal("", &config); err != nil {
		return nil, err
	}
	setFlagship(&config, isFlagship)

	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &config, nil
}

// WriteEnvExample writes every config env var with its default value.
func WriteEnvExample(w io.Writer) error {
	k := koanf.New(".")
	if err := k.Load(structs.Provider(DefaultConfig(), "koanf"), nil); err != nil {
		return err
	}

	names, err := envNames(k)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		if _, err := fmt.Fprintf(w, "%s=%v\n", name, k.Get(names[name])); err != nil {
			return err
		}
	}
	return nil
}

// Helpers

type (
	validator interface{ Validate() error }
	defaulter interface{ SetDefaults() }
)

// eachField calls fn with a pointer to every exported field of c.
func eachField(c *Config, fn func(any)) {
	v := reflect.ValueOf(c).Elem()
	for i := range v.NumField() {
		if v.Type().Field(i).IsExported() {
			fn(v.Field(i).Addr().Interface())
		}
	}
}

func setFlagship(c *Config, isFlagship bool) {
	eachField(c, func(f any) {
		v := reflect.ValueOf(f).Elem()
		if v.Kind() != reflect.Struct {
			return
		}
		if field := v.FieldByName("IsFlagship"); field.IsValid() && field.Kind() == reflect.Bool && field.CanSet() {
			field.SetBool(isFlagship)
		}
	})
}

func envNames(k *koanf.Koanf) (map[string]string, error) {
	names := make(map[string]string)
	for _, key := range k.Keys() {
		name := strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		if other, ok := names[name]; ok {
			return nil, fmt.Errorf("config keys '%q' and '%q' both map to %s", other, key, name)
		}
		names[name] = key
	}
	return names, nil
}
