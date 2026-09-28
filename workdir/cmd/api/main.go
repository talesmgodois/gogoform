// Command api starts the HTTP API server.
//
//	@title			App API
//	@version		1.0
//	@description	REST API of the application.
//	@host			localhost:8080
//	@BasePath		/
//
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						X-API-Key
//	@description				API key of the tenant, returned when the tenant is created.
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				JWT returned by POST /auth/signin, sent as "Bearer <token>".
//
//	@securityDefinitions.basic	BasicAuth
//	@description				Username and password of a user: "Basic " + btoa(username + ":" + password).
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"app/cmd/api/handlers"
	"app/docs"
	"app/internal/config"
	"app/internal/database"
	"app/internal/db"
	"app/internal/logger"
	"app/internal/pkg/auth"
	"app/internal/pkg/auth/oidc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", config.DefaultPath, "path to the TOML configuration file")
	envPath := flag.String("env", config.DefaultEnvPath, "path to the dotenv file (ignored if missing)")
	flag.Parse()

	// Load .env before the config so its values override config.toml even when
	// the server is started without make (go run, IDE, compiled binary).
	if err := config.LoadDotEnv(*envPath); err != nil {
		return err
	}

	cfg, err := config.Init(*configPath)
	if err != nil {
		return err
	}

	log := logger.New(os.Stdout, cfg.Logger.Level, cfg.Server.Env)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbConn, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer dbConn.Close()
	log.Info("database connected", append([]any{"driver", dbConn.Driver}, dbConn.LogAttrs()...)...)

	if cfg.Database.AutoMigrateEnabled() {
		applied, err := database.Migrate(ctx, dbConn)
		for _, version := range applied {
			log.Info("database migration applied", "version", version)
		}
		if err != nil {
			return err
		}
		log.Info("database migrations up to date", "applied", len(applied))
	}

	queries := dbConn.Querier
	secret := jwtSecret(log, cfg.Auth)
	authSvc, err := newAuthService(ctx, log, queries, secret, cfg.Auth, cfg.OIDC)
	if err != nil {
		return err
	}
	services := handlers.NewServices(queries, authSvc)
	if cfg.OIDC.Enabled() {
		client, err := oidc.New(ctx, oidc.Config{
			IssuerURL:           cfg.OIDC.IssuerURL,
			ClientID:            cfg.OIDC.ClientID,
			ClientSecret:        cfg.OIDC.ClientSecret,
			RedirectURL:         cfg.OIDC.RedirectURL,
			Scopes:              cfg.OIDC.Scopes,
			AllowedEmailDomains: cfg.OIDC.AllowedEmailDomains,
		})
		if err != nil {
			return err
		}
		services = services.WithOIDC(handlers.OIDCOptions{Client: client, ProviderName: cfg.OIDC.ProviderName, FlowKey: secret})
		log.Info("oidc: sign-in enabled", "issuer", cfg.OIDC.IssuerURL, "provider", cfg.OIDC.ProviderName,
			"auto_create_users", cfg.OIDC.AutoCreateUsers, "default_role", cfg.OIDC.DefaultRole)
	}
	if !cfg.Auth.PasswordLoginEnabled {
		log.Info("auth: password sign-in disabled (AUTH_PASSWORD_LOGIN_ENABLED=false)")
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	docs.SwaggerInfo.Host = fmt.Sprintf("localhost:%d", cfg.Server.Port)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handlers.Routes(services, cfg.App),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	if !cfg.App.Enabled() {
		log.Warn("/app dashboard disabled: set APP_USERNAME and APP_PASSWORD to enable it")
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server starting", "addr", addr, "env", cfg.Server.Env, "log_level", cfg.Logger.Level)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("server shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// jwtSecret returns the secret signing the app's tokens (and the OIDC flow
// cookie). Without a configured one, a random secret is generated, so
// tokens do not survive a restart.
func jwtSecret(log *slog.Logger, cfg config.AuthConfig) []byte {
	secret := []byte(cfg.JWTSecret)
	if len(secret) > 0 {
		return secret
	}
	log.Warn("auth: AUTH_JWT_SECRET is not set; using a random secret, so tokens are invalidated on restart")
	secret = make([]byte, auth.MinSecretLen)
	// crypto/rand.Read never returns an error.
	_, _ = rand.Read(secret)
	return secret
}

// newAuthService builds the user authentication service and creates the
// bootstrap admin of cfg, if any.
func newAuthService(ctx context.Context, log *slog.Logger, q db.Querier, secret []byte, cfg config.AuthConfig, oidcCfg config.OIDCConfig) (*auth.Service, error) {
	opts := auth.Options{
		Secret:                secret,
		TokenTTL:              cfg.TokenTTL(),
		PasswordLoginDisabled: !cfg.PasswordLoginEnabled,
	}
	if oidcCfg.Enabled() {
		opts.External = auth.ExternalOptions{AutoCreateUsers: oidcCfg.AutoCreateUsers, DefaultRole: auth.Role(oidcCfg.DefaultRole)}
	}
	svc, err := auth.NewService(auth.NewUserStore(q), opts)
	if err != nil {
		return nil, err
	}
	if cfg.AdminUsername != "" {
		created, err := svc.EnsureUser(ctx, cfg.AdminUsername, cfg.AdminPassword, auth.RoleAdmin)
		if err != nil {
			return nil, fmt.Errorf("auth: bootstrap admin: %w", err)
		}
		if created {
			log.Info("auth: bootstrap admin created", "username", auth.NormalizeUsername(cfg.AdminUsername))
		}
	}
	return svc, nil
}
