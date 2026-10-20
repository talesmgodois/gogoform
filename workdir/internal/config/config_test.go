package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `
[server]
port = 8080
env = "development"

[logger]
level = "debug"

[database]
uri = "postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable"
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromFile(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8080 || cfg.Server.Env != "development" || cfg.Logger.Level != "debug" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if want := "postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable"; cfg.Database.URI != want {
		t.Fatalf("database.uri = %q, want %q", cfg.Database.URI, want)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("SERVER_ENV", "production")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("DATABASE_URL", "postgresql://app:secret@db:5433/other")
	t.Setenv("AUTH_JWT_SECRET", strings.Repeat("s", minJWTSecretLen))

	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9090 || cfg.Server.Env != "production" || cfg.Logger.Level != "error" {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
	if cfg.Database.URI != "postgresql://app:secret@db:5433/other" {
		t.Fatalf("DATABASE_URL override not applied: %q", cfg.Database.URI)
	}
}

func TestAuthConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.JWTSecret != "" || cfg.Auth.TokenTTL() != time.Hour {
		t.Fatalf("unexpected auth defaults: %+v", cfg.Auth)
	}

	secret := strings.Repeat("s", minJWTSecretLen)
	t.Setenv("AUTH_JWT_SECRET", secret)
	t.Setenv("AUTH_TOKEN_TTL_MINUTES", "15")
	t.Setenv("AUTH_ADMIN_USERNAME", "root")
	t.Setenv("AUTH_ADMIN_PASSWORD", "change-me-now")
	cfg, err = Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.JWTSecret != secret || cfg.Auth.TokenTTL() != 15*time.Minute ||
		cfg.Auth.AdminUsername != "root" || cfg.Auth.AdminPassword != "change-me-now" {
		t.Fatalf("auth overrides not applied: %+v", cfg.Auth)
	}
}

func TestFeatureDefaults(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"development", true},
		{"production", false},
		{"staging", false},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("SERVER_ENV", tt.env)
			t.Setenv("AUTH_JWT_SECRET", strings.Repeat("s", minJWTSecretLen))
			cfg, err := Load(writeConfig(t, sample))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Features.PublicTenantSignupEnabled(); got != tt.want {
				t.Errorf("PublicTenantSignupEnabled = %v, want %v", got, tt.want)
			}
			if got := cfg.Features.SamplesEnabled(); got != tt.want {
				t.Errorf("SamplesEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFeatureOverrides(t *testing.T) {
	// The file disables the flags in development.
	cfg, err := Load(writeConfig(t, sample+"\n[features]\npublic_tenant_signup = false\nsamples_enabled = false\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Features.PublicTenantSignupEnabled() || cfg.Features.SamplesEnabled() {
		t.Fatalf("file overrides not applied: %+v", cfg.Features)
	}

	// The environment enables them in production, over the file.
	t.Setenv("SERVER_ENV", "production")
	t.Setenv("AUTH_JWT_SECRET", strings.Repeat("s", minJWTSecretLen))
	t.Setenv("FEATURE_PUBLIC_TENANT_SIGNUP", "true")
	t.Setenv("FEATURE_SAMPLES", "1")
	cfg, err = Load(writeConfig(t, sample+"\n[features]\nsamples_enabled = false\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Features.PublicTenantSignupEnabled() || !cfg.Features.SamplesEnabled() {
		t.Fatalf("env overrides not applied: %+v", cfg.Features)
	}
}

func TestStripeConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Stripe.Enabled() {
		t.Fatalf("stripe enabled without configuration: %+v", cfg.Stripe)
	}

	// The file sets part of the section and the environment completes it.
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_123")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_123")
	cfg, err = Load(writeConfig(t, sample+"\n[app]\nbase_url = \"https://forms.example.com\"\n[stripe]\nprice_id = \"price_123\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Stripe.Enabled() || cfg.Stripe.SecretKey != "sk_test_123" || cfg.Stripe.WebhookSecret != "whsec_123" ||
		cfg.Stripe.PriceID != "price_123" || cfg.App.BaseURL != "https://forms.example.com" {
		t.Fatalf("stripe config not applied: %+v %+v", cfg.Stripe, cfg.App)
	}
}

func TestMailConfig(t *testing.T) {
	// Without a host, mail is disabled and nothing else is required, not
	// even the base URL.
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mail.Enabled() || cfg.Mail.Port != 587 {
		t.Fatalf("unexpected mail defaults: %+v", cfg.Mail)
	}

	// The file sets part of the section and the environment completes it.
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_PASSWORD", "secret")
	t.Setenv("APP_BASE_URL", "https://forms.example.com")
	cfg, err = Load(writeConfig(t, sample+"\n[mail]\nsmtp_user = \"apikey\"\nfrom = \"GoGoForm <no-reply@example.com>\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Mail.Enabled() || cfg.Mail.Host != "smtp.example.com" || cfg.Mail.Port != 465 ||
		cfg.Mail.User != "apikey" || cfg.Mail.Password != "secret" ||
		cfg.Mail.From != "GoGoForm <no-reply@example.com>" || cfg.App.BaseURL != "https://forms.example.com" {
		t.Fatalf("mail config not applied: %+v %+v", cfg.Mail, cfg.App)
	}
}

func TestSecurityDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Security.MinFormSeconds != 2 {
		t.Fatalf("MinFormSeconds = %d, want 2", cfg.Security.MinFormSeconds)
	}
	if cfg.Security.RateLimit.Signin != (RateLimitGroupConfig{PerMinute: 5, Burst: 5}) {
		t.Fatalf("Signin = %+v", cfg.Security.RateLimit.Signin)
	}
	if cfg.Security.RateLimit.PublicSubmission != (RateLimitGroupConfig{PerMinute: 20, Burst: 20}) {
		t.Fatalf("PublicSubmission = %+v", cfg.Security.RateLimit.PublicSubmission)
	}
	if prefixes, err := cfg.Security.TrustedProxyPrefixes(); err != nil || len(prefixes) != 0 {
		t.Fatalf("TrustedProxyPrefixes = %v, %v, want none", prefixes, err)
	}
}

func TestSecurityOverrides(t *testing.T) {
	content := sample + "\n[security.rate_limit.signup]\nper_minute = 1\nburst = 2\n"
	t.Setenv("SECURITY_TRUSTED_PROXIES", " 10.0.0.0/8 , 192.168.1.5 ")
	t.Setenv("SECURITY_MIN_FORM_SECONDS", "5")
	t.Setenv("TURNSTILE_SECRET", "secret")
	t.Setenv("TURNSTILE_SITE_KEY", "site-key")

	cfg, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Security.MinFormSeconds != 5 || cfg.Security.TurnstileSecret != "secret" || cfg.Security.TurnstileSiteKey != "site-key" {
		t.Fatalf("security overrides not applied: %+v", cfg.Security)
	}
	if cfg.Security.RateLimit.Signup != (RateLimitGroupConfig{PerMinute: 1, Burst: 2}) {
		t.Fatalf("Signup = %+v", cfg.Security.RateLimit.Signup)
	}

	prefixes, err := cfg.Security.TrustedProxyPrefixes()
	if err != nil {
		t.Fatalf("TrustedProxyPrefixes: %v", err)
	}
	if len(prefixes) != 2 {
		t.Fatalf("TrustedProxyPrefixes = %v, want 2 entries", prefixes)
	}
	addr := netip.MustParseAddr("10.1.2.3")
	if !prefixes[0].Contains(addr) {
		t.Fatalf("%v does not contain %v", prefixes[0], addr)
	}
	bareHost := netip.MustParseAddr("192.168.1.5")
	if !prefixes[1].Contains(bareHost) || prefixes[1].Bits() != 32 {
		t.Fatalf("bare IP prefix = %v, want a /32 containing %v", prefixes[1], bareHost)
	}
}

func TestAppBaseURLOptionalWithoutStripeOrMail(t *testing.T) {
	t.Setenv("APP_BASE_URL", "not a url")
	if _, err := Load(writeConfig(t, sample)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		env     map[string]string
	}{
		{name: "invalid toml", content: "[server\nport = "},
		{name: "invalid port env", content: sample, env: map[string]string{"SERVER_PORT": "abc"}},
		{name: "port out of range", content: sample, env: map[string]string{"SERVER_PORT": "70000"}},
		{name: "invalid level", content: sample, env: map[string]string{"LOG_LEVEL": "verbose"}},
		{name: "missing database uri", content: "[server]\nport = 8080\n"},
		{name: "unsupported database scheme", content: sample, env: map[string]string{"DATABASE_URL": "mysql://u:p@localhost/db"}},
		{name: "short jwt secret", content: sample, env: map[string]string{"AUTH_JWT_SECRET": "too-short"}},
		{name: "production without jwt secret", content: sample, env: map[string]string{"SERVER_ENV": "production"}},
		{name: "staging without jwt secret", content: sample, env: map[string]string{"SERVER_ENV": "staging"}},
		{name: "zero token ttl", content: sample, env: map[string]string{"AUTH_TOKEN_TTL_MINUTES": "0"}},
		{name: "admin username without password", content: sample, env: map[string]string{"AUTH_ADMIN_USERNAME": "admin"}},
		{name: "invalid feature flag env", content: sample, env: map[string]string{"FEATURE_SAMPLES": "maybe"}},
		{name: "partial stripe config", content: sample, env: map[string]string{"STRIPE_SECRET_KEY": "sk_test_123", "STRIPE_PRICE_ID": "price_123"}},
		{name: "stripe without app base url", content: sample, env: stripeEnv("")},
		{name: "stripe with relative app base url", content: sample, env: stripeEnv("/app")},
		{name: "stripe with non-http app base url", content: sample, env: stripeEnv("ftp://forms.example.com")},
		{name: "mail without from", content: sample, env: mailEnv("", "https://forms.example.com")},
		{name: "mail with invalid from", content: sample, env: mailEnv("not an address", "https://forms.example.com")},
		{name: "mail without app base url", content: sample, env: mailEnv("no-reply@example.com", "")},
		{name: "mail with relative app base url", content: sample, env: mailEnv("no-reply@example.com", "/app")},
		{name: "mail port out of range", content: sample, env: map[string]string{
			"SMTP_HOST": "smtp.example.com", "SMTP_PORT": "0", "MAIL_FROM": "no-reply@example.com", "APP_BASE_URL": "https://forms.example.com",
		}},
		{name: "invalid smtp port env", content: sample, env: map[string]string{"SMTP_PORT": "abc"}},
		{name: "database uri without host", content: sample, env: map[string]string{"DATABASE_URL": "postgres:///app_db"}},
		{name: "invalid trusted proxy", content: sample, env: map[string]string{"SECURITY_TRUSTED_PROXIES": "not-an-ip"}},
		{name: "negative min form seconds", content: sample, env: map[string]string{"SECURITY_MIN_FORM_SECONDS": "-1"}},
		{name: "turnstile secret without site key", content: sample, env: map[string]string{"TURNSTILE_SECRET": "secret"}},
		{name: "rate limit per_minute zero", content: sample + "\n[security.rate_limit.signin]\nper_minute = 0\nburst = 5\n"},
		{name: "rate limit burst zero", content: sample + "\n[security.rate_limit.signup]\nper_minute = 3\nburst = 0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := Load(writeConfig(t, tt.content)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// stripeEnv returns a complete Stripe environment with the given APP_BASE_URL.
func stripeEnv(baseURL string) map[string]string {
	return map[string]string{
		"STRIPE_SECRET_KEY":     "sk_test_123",
		"STRIPE_WEBHOOK_SECRET": "whsec_123",
		"STRIPE_PRICE_ID":       "price_123",
		"APP_BASE_URL":          baseURL,
	}
}

// mailEnv returns an SMTP environment with the given MAIL_FROM and
// APP_BASE_URL.
func mailEnv(from, baseURL string) map[string]string {
	return map[string]string{
		"SMTP_HOST":    "smtp.example.com",
		"MAIL_FROM":    from,
		"APP_BASE_URL": baseURL,
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSingleton(t *testing.T) {
	path := writeConfig(t, sample)

	first, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if second := GetConfig(); second != first {
		t.Fatal("GetConfig returned a different instance")
	}
}
