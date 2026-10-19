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
	Auth     AuthConfig     `toml:"auth"`
	Features FeaturesConfig `toml:"features"`
	Stripe   StripeConfig   `toml:"stripe"`
}

// StripeConfig holds the Stripe billing settings. Billing is enabled only
// when the secret key, webhook secret and price are all set.
type StripeConfig struct {
	SecretKey     string `toml:"secret_key"     env:"STRIPE_SECRET_KEY"`
	WebhookSecret string `toml:"webhook_secret" env:"STRIPE_WEBHOOK_SECRET"`
	// PriceID is the recurring price of the pro plan.
	PriceID string `toml:"price_id" env:"STRIPE_PRICE_ID"`
	// AppBaseURL is the public origin used to build the Checkout and
	// Customer Portal return URLs, e.g. "https://forms.example.com".
	AppBaseURL string `toml:"app_base_url" env:"APP_BASE_URL"`
}

// Enabled reports whether the billing routes are mounted.
func (c StripeConfig) Enabled() bool {
	return c.SecretKey != "" && c.WebhookSecret != "" && c.PriceID != ""
}

func (c StripeConfig) validate() error {
	set := 0
	for _, v := range []string{c.SecretKey, c.WebhookSecret, c.PriceID} {
		if v != "" {
			set++
		}
	}
	if set == 0 {
		return nil
	}
	if set != 3 {
		return fmt.Errorf("secret_key, webhook_secret and price_id must be set together")
	}
	u, err := url.Parse(c.AppBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("app_base_url: must be an absolute http(s) URL (or provide APP_BASE_URL)")
	}
	return nil
}

// FeaturesConfig toggles endpoints that are only meant for development. A
// flag left unset in both the file and the environment defaults to true when
// server.env is "development" and to false otherwise.
type FeaturesConfig struct {
	// PublicTenantSignup mounts the unauthenticated POST /tenants.
	PublicTenantSignup *bool `toml:"public_tenant_signup" env:"FEATURE_PUBLIC_TENANT_SIGNUP"`
	// Samples mounts the /samples/* policy examples.
	Samples *bool `toml:"samples_enabled" env:"FEATURE_SAMPLES"`
}

// PublicTenantSignupEnabled reports whether POST /tenants is mounted.
func (c FeaturesConfig) PublicTenantSignupEnabled() bool {
	return c.PublicTenantSignup != nil && *c.PublicTenantSignup
}

// SamplesEnabled reports whether the /samples/* routes are mounted.
func (c FeaturesConfig) SamplesEnabled() bool {
	return c.Samples != nil && *c.Samples
}

// resolveDefaults sets every unset flag to whether env is "development".
func (c *FeaturesConfig) resolveDefaults(env string) {
	dev := strings.EqualFold(env, "development")
	for _, f := range []**bool{&c.PublicTenantSignup, &c.Samples} {
		if *f == nil {
			v := dev
			*f = &v
		}
	}
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
	cfg.Features.resolveDefaults(cfg.Server.Env)

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
		case reflect.Pointer:
			if fv.Type().Elem().Kind() != reflect.Bool {
				return fmt.Errorf("env %s: unsupported field kind %s", key, fv.Type())
			}
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("env %s: invalid boolean %q", key, raw)
			}
			fv.Set(reflect.ValueOf(&b))
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
	if c.Auth.JWTSecret != "" && len(c.Auth.JWTSecret) < minJWTSecretLen {
		return fmt.Errorf("auth.jwt_secret: must be at least %d bytes", minJWTSecretLen)
	}
	if c.Auth.TokenTTLMinutes < 1 {
		return fmt.Errorf("auth.token_ttl_minutes: must be >= 1")
	}
	if (c.Auth.AdminUsername == "") != (c.Auth.AdminPassword == "") {
		return fmt.Errorf("auth: admin_username and admin_password must be set together")
	}
	if err := c.Stripe.validate(); err != nil {
		return fmt.Errorf("stripe: %w", err)
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
