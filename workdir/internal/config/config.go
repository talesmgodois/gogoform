// Package config loads application configuration from a TOML file and
// applies environment variable overrides. The loaded configuration is exposed
// as a process-wide singleton.
package config

import (
	"fmt"
	"net/mail"
	"net/netip"
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
	App      AppConfig      `toml:"app"`
	Database DatabaseConfig `toml:"database"`
	Auth     AuthConfig     `toml:"auth"`
	Features FeaturesConfig `toml:"features"`
	Stripe   StripeConfig   `toml:"stripe"`
	Mail     MailConfig     `toml:"mail"`
	Security SecurityConfig `toml:"security"`
}

// AppConfig holds settings shared by the features that build absolute links
// back to the application (Stripe return URLs, links in emails).
type AppConfig struct {
	// BaseURL is the public origin of the application, e.g.
	// "https://forms.example.com". It is required when Stripe or mail is
	// enabled.
	BaseURL string `toml:"base_url" env:"APP_BASE_URL"`
}

func (c AppConfig) validate() error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("base_url: must be an absolute http(s) URL (or provide APP_BASE_URL)")
	}
	return nil
}

// MailConfig holds the SMTP settings of transactional email. Email is sent
// through SMTP only when Host is set; otherwise messages are only logged.
type MailConfig struct {
	Host     string `toml:"smtp_host"     env:"SMTP_HOST"`
	Port     int    `toml:"smtp_port"     env:"SMTP_PORT"`
	User     string `toml:"smtp_user"     env:"SMTP_USER"`
	Password string `toml:"smtp_password" env:"SMTP_PASSWORD"`
	// From is the sender address, e.g. "GoGoForm <no-reply@example.com>".
	From string `toml:"from" env:"MAIL_FROM"`
}

// Enabled reports whether email is sent through SMTP.
func (c MailConfig) Enabled() bool {
	return c.Host != ""
}

func (c MailConfig) validate() error {
	if !c.Enabled() {
		return nil
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("smtp_port: %d out of range 1-65535", c.Port)
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return fmt.Errorf("from: must be a valid address (or provide MAIL_FROM)")
	}
	return nil
}

// StripeConfig holds the Stripe billing settings. Billing is enabled only
// when the secret key, webhook secret and price are all set.
type StripeConfig struct {
	SecretKey     string `toml:"secret_key"     env:"STRIPE_SECRET_KEY"`
	WebhookSecret string `toml:"webhook_secret" env:"STRIPE_WEBHOOK_SECRET"`
	// PriceID is the recurring price of the pro plan.
	PriceID string `toml:"price_id" env:"STRIPE_PRICE_ID"`
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

// SecurityConfig holds the abuse-protection settings: the rate limits of the
// sensitive routes, the reverse proxies trusted to supply the real client
// IP, and the public-form spam checks (minimum fill time, Cloudflare
// Turnstile). It has no bearing on authorization, only on abuse.
type SecurityConfig struct {
	// TrustedProxies is a comma-separated list of CIDR ranges (or bare IPs,
	// treated as a single-host range), e.g. "10.0.0.0/8,172.16.0.0/12". Its
	// X-Forwarded-For header is trusted to carry the real client IP only
	// when the direct peer (RemoteAddr) matches one of these ranges;
	// otherwise the header is attacker-controlled and trivial to forge, so
	// it is ignored both for rate limiting and for submission metadata.
	// Leave unset unless a reverse proxy you control sits in front of the app.
	TrustedProxies string `toml:"trusted_proxies" env:"SECURITY_TRUSTED_PROXIES"`
	// RateLimit holds the token-bucket settings of every rate-limited route
	// group. These are tuning knobs, not secrets, so they are only read from
	// the TOML file, not the environment.
	RateLimit RateLimitConfig `toml:"rate_limit"`
	// MinFormSeconds is the minimum time a respondent must take to fill a
	// public form in; a submission completed faster is rejected as
	// automated.
	MinFormSeconds int `toml:"min_form_seconds" env:"SECURITY_MIN_FORM_SECONDS"`
	// TurnstileSecret enables server-side verification of Cloudflare
	// Turnstile on public form submissions when set. TurnstileSiteKey is the
	// matching public key, rendered on the form page to collect the token.
	TurnstileSecret  string `toml:"turnstile_secret"   env:"TURNSTILE_SECRET"`
	TurnstileSiteKey string `toml:"turnstile_site_key" env:"TURNSTILE_SITE_KEY"`
}

// TrustedProxyPrefixes parses TrustedProxies into CIDR prefixes. A bare IP
// (no "/") is treated as a single-host range (a /32 or /128 prefix).
func (c SecurityConfig) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, raw := range strings.Split(c.TrustedProxies, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q", raw)
			}
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", raw)
		}
		prefixes = append(prefixes, p)
	}
	return prefixes, nil
}

func (c SecurityConfig) validate() error {
	if _, err := c.TrustedProxyPrefixes(); err != nil {
		return fmt.Errorf("trusted_proxies: %w", err)
	}
	if err := c.RateLimit.validate(); err != nil {
		return err
	}
	if c.MinFormSeconds < 0 {
		return fmt.Errorf("min_form_seconds: must be >= 0")
	}
	if (c.TurnstileSecret == "") != (c.TurnstileSiteKey == "") {
		return fmt.Errorf("turnstile_secret and turnstile_site_key must be set together")
	}
	return nil
}

// RateLimitConfig holds the token-bucket settings of every rate-limited
// route group: user sign-in (API and /app), sign-up, password reset
// requests, and the public form submission and file upload endpoints.
type RateLimitConfig struct {
	Signin           RateLimitGroupConfig `toml:"signin"`
	Signup           RateLimitGroupConfig `toml:"signup"`
	ForgotPassword   RateLimitGroupConfig `toml:"forgot_password"`
	PublicSubmission RateLimitGroupConfig `toml:"public_submission"`
	PublicUpload     RateLimitGroupConfig `toml:"public_upload"`
}

func (c RateLimitConfig) validate() error {
	for _, g := range []struct {
		name string
		cfg  RateLimitGroupConfig
	}{
		{"signin", c.Signin}, {"signup", c.Signup}, {"forgot_password", c.ForgotPassword},
		{"public_submission", c.PublicSubmission}, {"public_upload", c.PublicUpload},
	} {
		if err := g.cfg.validate(g.name); err != nil {
			return err
		}
	}
	return nil
}

// RateLimitGroupConfig is a token bucket: Burst requests may be made at
// once by a client IP, refilling at PerMinute requests per minute.
type RateLimitGroupConfig struct {
	PerMinute int `toml:"per_minute"`
	Burst     int `toml:"burst"`
}

func (c RateLimitGroupConfig) validate(name string) error {
	if c.PerMinute < 1 {
		return fmt.Errorf("rate_limit.%s.per_minute: must be >= 1", name)
	}
	if c.Burst < 1 {
		return fmt.Errorf("rate_limit.%s.burst: must be >= 1", name)
	}
	return nil
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
	cfg.Mail.Port = 587
	cfg.Security.MinFormSeconds = 2
	cfg.Security.RateLimit = RateLimitConfig{
		Signin:           RateLimitGroupConfig{PerMinute: 5, Burst: 5},
		Signup:           RateLimitGroupConfig{PerMinute: 3, Burst: 3},
		ForgotPassword:   RateLimitGroupConfig{PerMinute: 3, Burst: 3},
		PublicSubmission: RateLimitGroupConfig{PerMinute: 20, Burst: 20},
		PublicUpload:     RateLimitGroupConfig{PerMinute: 30, Burst: 30},
	}
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
	switch {
	case c.Auth.JWTSecret == "" && !strings.EqualFold(c.Server.Env, "development"):
		return fmt.Errorf("auth.jwt_secret: must be set (via AUTH_JWT_SECRET) when server.env is not \"development\"")
	case c.Auth.JWTSecret != "" && len(c.Auth.JWTSecret) < minJWTSecretLen:
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
	if err := c.Mail.validate(); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if err := c.Security.validate(); err != nil {
		return fmt.Errorf("security: %w", err)
	}
	// The Checkout/Portal return URLs and the links in emails are absolute.
	if c.Stripe.Enabled() || c.Mail.Enabled() {
		if err := c.App.validate(); err != nil {
			return fmt.Errorf("app: %w", err)
		}
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
