// Package config loads application configuration from a TOML file and
// applies environment variable overrides. The loaded configuration is exposed
// as a process-wide singleton.
package config

import (
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// DefaultPath is the configuration file used when no path is provided.
const DefaultPath = "./config.toml"

// Config holds the application configuration.
type Config struct {
	Server struct {
		Port int    `toml:"port" env:"SERVER_PORT"`
		Env  string `toml:"env"  env:"SERVER_ENV"`
	} `toml:"server"`
	Logger struct {
		Level string `toml:"level" env:"LOG_LEVEL"` // "debug", "info", "warn", "error"
	} `toml:"logger"`
	Database DatabaseConfig `toml:"database"`
}

// DatabaseConfig holds the database connection settings.
type DatabaseConfig struct {
	URI string `toml:"uri" env:"DATABASE_URL"` // e.g. "postgres://user:pass@host:5432/db?sslmode=disable"
}

var (
	once     sync.Once
	instance *Config
	loadErr  error
)

// Init loads the configuration from path exactly once. Subsequent calls
// return the already loaded configuration and ignore path.
func Init(path string) (*Config, error) {
	once.Do(func() {
		instance, loadErr = Load(path)
	})
	return instance, loadErr
}

// GetConfig returns the configuration singleton, loading it from DefaultPath
// if Init has not been called yet. It panics if the configuration cannot be
// loaded; call Init first to handle the error explicitly.
func GetConfig() *Config {
	cfg, err := Init(DefaultPath)
	if err != nil {
		panic(fmt.Sprintf("config: %v", err))
	}
	return cfg
}

// Load reads the TOML file at path, applies environment variable overrides
// and validates the result. It does not touch the singleton.
func Load(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := applyEnv(reflect.ValueOf(cfg).Elem()); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func defaults() *Config {
	cfg := &Config{}
	cfg.Server.Port = 8080
	cfg.Server.Env = "development"
	cfg.Logger.Level = "info"
	return cfg
}

// applyEnv walks v recursively and overrides every field tagged with `env`
// whose environment variable is set.
func applyEnv(v reflect.Value) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field, fv := t.Field(i), v.Field(i)

		if fv.Kind() == reflect.Struct {
			if err := applyEnv(fv); err != nil {
				return err
			}
			continue
		}

		key := field.Tag.Get("env")
		if key == "" {
			continue
		}
		raw, ok := os.LookupEnv(key)
		if !ok {
			continue
		}

		switch fv.Kind() {
		case reflect.String:
			fv.SetString(raw)
		case reflect.Int:
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("env %s: invalid integer %q", key, raw)
			}
			fv.SetInt(int64(n))
		default:
			return fmt.Errorf("env %s: unsupported field kind %s", key, fv.Kind())
		}
	}
	return nil
}

func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port: %d out of range 1-65535", c.Server.Port)
	}
	switch strings.ToLower(c.Logger.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logger.level: unsupported value %q", c.Logger.Level)
	}
	if err := validateDatabaseURI(c.Database.URI); err != nil {
		return fmt.Errorf("database.uri: %w", err)
	}
	return nil
}

func validateDatabaseURI(uri string) error {
	if uri == "" {
		return fmt.Errorf("must be set (or provide DATABASE_URL)")
	}
	u, err := url.Parse(uri)
	if err != nil {
		// url.Error embeds the full URI, which may contain credentials.
		return fmt.Errorf("invalid URI")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("unsupported scheme %q, expected postgres or postgresql", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}
