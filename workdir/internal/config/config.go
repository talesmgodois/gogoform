// Package config loads application configuration from a TOML file and
// applies environment variable overrides. The loaded configuration is exposed
// as a process-wide singleton.
package config

import (
	"fmt"
	"net/url"
	"os"
	"reflect"
	"slices"
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
	OIDC     OIDCConfig     `toml:"oidc"`
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
	// PasswordLoginEnabled gates password sign-in, sign-up and HTTP Basic
	// auth on the API. When false, only OIDC sign-in works; the bootstrap
	// admin above is still created.
	PasswordLoginEnabled bool `toml:"password_login_enabled" env:"AUTH_PASSWORD_LOGIN_ENABLED"`
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

// OIDCConfig holds the settings of signing in through an external OpenID
// Connect provider. OIDC is fully optional: with IssuerURL, ClientID and
// RedirectURL all unset, the app behaves exactly as without this feature.
type OIDCConfig struct {
	IssuerURL string `toml:"issuer_url"    env:"OIDC_ISSUER_URL"`
	ClientID  string `toml:"client_id"     env:"OIDC_CLIENT_ID"`
	// ClientSecret is optional for public clients that only use PKCE.
	ClientSecret string `toml:"client_secret" env:"OIDC_CLIENT_SECRET"`
	RedirectURL  string `toml:"redirect_url"  env:"OIDC_REDIRECT_URL"`
	// ProviderName labels the "Sign in with <name>" button.
	ProviderName string `toml:"provider_name" env:"OIDC_PROVIDER_NAME"`
	// Scopes requested from the provider; "openid" is always included even
	// if omitted here.
	Scopes []string `toml:"scopes" env:"OIDC_SCOPES"`
	// AllowedEmailDomains, when set, restricts sign-in to users with a
	// verified email in one of these domains.
	AllowedEmailDomains []string `toml:"allowed_email_domains" env:"OIDC_ALLOWED_EMAIL_DOMAINS"`
	// AutoCreateUsers creates a local user on first login. When false, only
	// identities already linked to a user may sign in.
	AutoCreateUsers bool `toml:"auto_create_users" env:"OIDC_AUTO_CREATE_USERS"`
	// DefaultRole is the role assigned to users created through OIDC.
	DefaultRole string `toml:"default_role" env:"OIDC_DEFAULT_ROLE"`
}

// Enabled reports whether OIDC sign-in is configured. IssuerURL, ClientID
// and RedirectURL must all be set together; validate() rejects a partial
// configuration.
func (c OIDCConfig) Enabled() bool {
	return c.IssuerURL != "" && c.ClientID != "" && c.RedirectURL != ""
}

// configuredFields counts how many of IssuerURL, ClientID and RedirectURL
// are set, to detect a partial configuration.
func (c OIDCConfig) configuredFields() int {
	n := 0
	if c.IssuerURL != "" {
		n++
	}
	if c.ClientID != "" {
		n++
	}
	if c.RedirectURL != "" {
		n++
	}
	return n
}

// normalize trims and deduplicates Scopes and AllowedEmailDomains, and makes
// sure Scopes always includes "openid". It runs unconditionally, whether or
// not OIDC is enabled, so the fields stay well-formed either way.
func (c *OIDCConfig) normalize() {
	c.Scopes = normalizeStringList(c.Scopes, false)
	if !slices.Contains(c.Scopes, "openid") {
		c.Scopes = append([]string{"openid"}, c.Scopes...)
	}
	c.AllowedEmailDomains = normalizeStringList(c.AllowedEmailDomains, true)
}

// normalizeStringList trims whitespace, drops empty entries and deduplicates
// while preserving order. When lower is true, entries are lowercased first.
func normalizeStringList(values []string, lower bool) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if lower {
			v = strings.ToLower(v)
		}
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
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
	// AutoMigrate applies pending migrations at startup. Unset, it follows
	// the engine; see AutoMigrateEnabled.
	AutoMigrate *bool `toml:"auto_migrate" env:"DATABASE_AUTO_MIGRATE"`
}

// AutoMigrateEnabled reports whether pending migrations are applied at
// startup. When AutoMigrate is unset it defaults to true for SQLite, so a
// single binary works with zero setup, and to false for PostgreSQL, whose
// operators usually run migrations as a separate deploy step.
func (c DatabaseConfig) AutoMigrateEnabled() bool {
	if c.AutoMigrate != nil {
		return *c.AutoMigrate
	}
	return c.Driver() == DriverSQLite
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
	cfg.OIDC.normalize()

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
	cfg.Auth.PasswordLoginEnabled = true
	cfg.Database.SQLiteBusyTimeoutMS = 5000
	cfg.Database.SQLiteJournalMode = "WAL"
	cfg.OIDC.ProviderName = "SSO"
	cfg.OIDC.Scopes = []string{"openid", "email", "profile"}
	cfg.OIDC.AutoCreateUsers = true
	cfg.OIDC.DefaultRole = defaultOIDCRole
	return cfg
}

// defaultOIDCRole and oidcValidRoles mirror auth.DefaultRole and
// auth.AllRoles. They are duplicated here, rather than imported, because
// internal/pkg/auth transitively imports internal/config (through
// internal/database), and importing it back would create an import cycle.
const defaultOIDCRole = "BASIC"

var oidcValidRoles = []string{"ADMIN", "FORM_CREATOR", "BASIC"}

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
		case reflect.Bool:
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("env %s: invalid boolean %q", key, raw)
			}
			fv.SetBool(b)
		case reflect.Pointer:
			// *bool distinguishes "unset" from false, for defaults that depend on other settings.
			if fv.Type().Elem().Kind() != reflect.Bool {
				return fmt.Errorf("env %s: unsupported field kind %s", key, fv.Kind())
			}
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("env %s: invalid boolean %q", key, raw)
			}
			fv.Set(reflect.ValueOf(&b))
		case reflect.Slice:
			if fv.Type().Elem().Kind() != reflect.String {
				return fmt.Errorf("env %s: unsupported field kind %s", key, fv.Kind())
			}
			fv.Set(reflect.ValueOf(splitCSV(raw)))
		default:
			return fmt.Errorf("env %s: unsupported field kind %s", key, fv.Kind())
		}
	}
	return nil
}

// splitCSV splits a comma-separated environment value into trimmed,
// non-empty parts.
func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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
	if err := c.OIDC.validate(); err != nil {
		return err
	}
	return nil
}

// validate checks the OIDC configuration. It never includes ClientSecret in
// any returned error.
func (c OIDCConfig) validate() error {
	if n := c.configuredFields(); n != 0 && n != 3 {
		return fmt.Errorf("oidc: issuer_url, client_id and redirect_url must be set together")
	}
	if !c.Enabled() {
		return nil
	}
	if err := validateAbsoluteURL(c.IssuerURL); err != nil {
		return fmt.Errorf("oidc.issuer_url: %w", err)
	}
	if err := validateAbsoluteURL(c.RedirectURL); err != nil {
		return fmt.Errorf("oidc.redirect_url: %w", err)
	}
	if !slices.Contains(oidcValidRoles, c.DefaultRole) {
		return fmt.Errorf("oidc.default_role: unsupported value %q", c.DefaultRole)
	}
	return nil
}

// validateAbsoluteURL requires an absolute http(s) URL with a host.
func validateAbsoluteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported scheme %q, expected http or https", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
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
