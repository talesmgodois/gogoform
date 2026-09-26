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
	"time"

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
	App      AppConfig      `toml:"app"`
	Auth     AuthConfig     `toml:"auth"`
}

// minJWTSecretLen is the minimum length of AuthConfig.JWTSecret: 256 bits,
// the size of the HS256 key.
const minJWTSecretLen = 32

// AuthConfig holds the settings of user authentication.
type AuthConfig struct {
	// JWTSecret signs the access tokens. When unset, a random secret is
	// generated at startup, so tokens do not survive a restart.
	JWTSecret string `toml:"jwt_secret" env:"AUTH_JWT_SECRET"`
	// TokenTTLMinutes is how long access tokens are valid.
	TokenTTLMinutes int `toml:"token_ttl_minutes" env:"AUTH_TOKEN_TTL_MINUTES"`
	// AdminUsername and AdminPassword bootstrap an ADMIN user at startup
	// when no user with that username exists yet.
	AdminUsername string `toml:"admin_username" env:"AUTH_ADMIN_USERNAME"`
	AdminPassword string `toml:"admin_password" env:"AUTH_ADMIN_PASSWORD"`
}

// TokenTTL returns TokenTTLMinutes as a duration.
func (c AuthConfig) TokenTTL() time.Duration {
	return time.Duration(c.TokenTTLMinutes) * time.Minute
}

// AppConfig holds the HTTP Basic credentials of the read-only /app
// dashboard. The dashboard lists every tenant's data, so it is disabled
// while they are unset.
type AppConfig struct {
	Username string `toml:"username" env:"APP_USERNAME"`
	Password string `toml:"password" env:"APP_PASSWORD"`
}

// Enabled reports whether the /app dashboard credentials are configured.
func (c AppConfig) Enabled() bool {
	return c.Username != "" && c.Password != ""
}

// DatabaseConfig holds the database connection settings.
type DatabaseConfig struct {
	URI string `toml:"uri" env:"DATABASE_URL"` // e.g. "postgres://user:pass@host:5432/db?sslmode=disable"
	// SQLiteBusyTimeoutMS is how long SQLite writers wait for a lock before
	// failing with SQLITE_BUSY. Ignored on PostgreSQL.
	SQLiteBusyTimeoutMS int `toml:"sqlite_busy_timeout_ms" env:"DATABASE_SQLITE_BUSY_TIMEOUT_MS"`
	// SQLiteJournalMode is the SQLite journal mode: WAL, DELETE or TRUNCATE.
	// Ignored on PostgreSQL.
	SQLiteJournalMode string `toml:"sqlite_journal_mode" env:"DATABASE_SQLITE_JOURNAL_MODE"`
}

// Driver identifies a supported database engine.
type Driver string

// DriverPostgres and DriverSQLite identify the supported database engines.
const (
	DriverPostgres Driver = "postgres"
	DriverSQLite   Driver = "sqlite"
)

// Driver reports the database engine selected by the URI scheme. It returns
// an empty Driver if the scheme is missing or unrecognized.
func (c DatabaseConfig) Driver() Driver {
	u, err := url.Parse(c.URI)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		return DriverPostgres
	case "sqlite":
		return DriverSQLite
	default:
		return ""
	}
}

// SQLitePath returns the file path encoded in a `sqlite:` URI, supporting
// dbmate's forms: "sqlite:./data/app.db" (relative), "sqlite:/abs/path.db"
// and "sqlite:///abs/path.db" (absolute), and "sqlite::memory:" (tests). It
// returns "" if the URI is not a sqlite URI or carries no path.
func (c DatabaseConfig) SQLitePath() string {
	u, err := url.Parse(c.URI)
	if err != nil || u.Scheme != "sqlite" {
		return ""
	}
	if u.Opaque != "" {
		return u.Opaque
	}
	return u.Path
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
	cfg.Auth.TokenTTLMinutes = 60
	cfg.Database.SQLiteBusyTimeoutMS = 5000
	cfg.Database.SQLiteJournalMode = "WAL"
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
	if c.Database.SQLiteBusyTimeoutMS < 0 {
		return fmt.Errorf("database.sqlite_busy_timeout_ms: must be >= 0")
	}
	switch strings.ToUpper(c.Database.SQLiteJournalMode) {
	case "WAL", "DELETE", "TRUNCATE":
	default:
		return fmt.Errorf("database.sqlite_journal_mode: unsupported value %q, expected one of: WAL, DELETE, TRUNCATE", c.Database.SQLiteJournalMode)
	}
	if (c.App.Username == "") != (c.App.Password == "") {
		return fmt.Errorf("app: username and password must be set together")
	}
	if strings.Contains(c.App.Username, ":") {
		return fmt.Errorf("app.username: must not contain ':'")
	}
	if c.Auth.JWTSecret != "" && len(c.Auth.JWTSecret) < minJWTSecretLen {
		return fmt.Errorf("auth.jwt_secret: must be at least %d bytes", minJWTSecretLen)
	}
	if c.Auth.TokenTTLMinutes < 1 {
		return fmt.Errorf("auth.token_ttl_minutes: must be >= 1")
	}
	if (c.Auth.AdminUsername == "") != (c.Auth.AdminPassword == "") {
		return fmt.Errorf("auth: admin_username and admin_password must be set together")
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
	switch u.Scheme {
	case "postgres", "postgresql":
		if u.Host == "" {
			return fmt.Errorf("missing host")
		}
	case "sqlite":
		path := u.Opaque
		if path == "" {
			path = u.Path
		}
		if path == "" {
			return fmt.Errorf("missing path")
		}
	default:
		return fmt.Errorf("unsupported scheme %q, expected one of: postgres, postgresql, sqlite", u.Scheme)
	}
	return nil
}
