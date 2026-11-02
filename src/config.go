package src

import (
	"errors"
	"fmt"
	"io"
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

	if err := checkRequired(&config, isFlagship); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &config, nil
}

// WriteEnvExample writes every config env var, grouped by which binary needs it.
//
// Required vars are left blank and optional vars are written with their value from SetDefaults().
func WriteEnvExample(w io.Writer) error {
	defaults := DefaultConfig()
	list, err := settings(&defaults)
	if err != nil {
		return err
	}
	slices.SortFunc(list, func(a, b setting) int { return strings.Compare(a.name, b.name) })

	sections := []struct{ title, required string }{
		{"Required by both flagship and escort", requiredAlways},
		{"Required by flagship only", requiredFlagship},
		{"Required by escort only", requiredEscort},
		{"Optional, shown with their default values", ""},
	}
	var blocks []string
	for _, section := range sections {
		block := "# " + section.title + "\n"
		empty := true
		var blockSb113 strings.Builder
		for _, s := range list {
			if s.required != section.required {
				continue
			}
			empty = false
			if s.required != "" {
				blockSb113.WriteString(s.name + "=\n")
				continue
			}
			fmt.Fprintf(&blockSb113, "%s=%v\n", s.name, s.value.Interface())
		}
		block += blockSb113.String()
		if !empty {
			blocks = append(blocks, block)
		}
	}
	_, err = io.WriteString(w, strings.Join(blocks, "\n"))
	return err
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

// Values of the `required` tag.
const (
	requiredAlways   = "always"
	requiredFlagship = "flagship"
	requiredEscort   = "escort"
)

// setting is a single config field that can be set through an env var.
type setting struct {
	name     string
	required string // empty when the setting is optional
	value    reflect.Value
}

func (s setting) requiredFor(isFlagship bool) bool {
	switch s.required {
	case requiredAlways:
		return true
	case requiredFlagship:
		return isFlagship
	case requiredEscort:
		return !isFlagship
	}
	return false
}

// settings lists every field of c that can be set through an env var.
//
// It returns an error if a `required` tag isn't one of the values above.
func settings(c *Config) ([]setting, error) {
	var list []setting
	var errs []error
	sections := reflect.ValueOf(c).Elem()
	for i := range sections.NumField() {
		sectionType := sections.Type().Field(i)
		section := sections.Field(i)
		if !sectionType.IsExported() || section.Kind() != reflect.Struct {
			continue
		}

		for j := range section.NumField() {
			field := section.Type().Field(j)
			key, _, _ := strings.Cut(field.Tag.Get("koanf"), ",")
			if key == "-" || !field.IsExported() {
				continue
			}
			if key == "" {
				key = field.Name
			}

			s := setting{name: envName(sectionType.Name + "." + key), value: section.Field(j)}
			if tag, ok := field.Tag.Lookup("required"); ok {
				switch tag {
				case requiredAlways, requiredFlagship, requiredEscort:
					s.required = tag
				default:
					errs = append(errs, fmt.Errorf("%s has unknown required tag %q", s.name, tag))
					continue
				}
			}
			list = append(list, s)
		}
	}
	return list, errors.Join(errs...)
}

// checkRequired returns an error for every required setting of this role that has no value.
func checkRequired(c *Config, isFlagship bool) error {
	list, err := settings(c)
	if err != nil {
		return err
	}

	var errs []error
	for _, s := range list {
		if s.requiredFor(isFlagship) && isEmpty(s.value) {
			errs = append(errs, fmt.Errorf("%s is required", s.name))
		}
	}
	return errors.Join(errs...)
}

func isEmpty(v reflect.Value) bool {
	if v.Kind() == reflect.String {
		return strings.TrimSpace(v.String()) == ""
	}
	return v.IsZero()
}

func envName(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func envNames(k *koanf.Koanf) (map[string]string, error) {
	names := make(map[string]string)
	for _, key := range k.Keys() {
		name := envName(key)
		if other, ok := names[name]; ok {
			return nil, fmt.Errorf("config keys '%q' and '%q' both map to %s", other, key, name)
		}
		names[name] = key
	}
	return names, nil
}
