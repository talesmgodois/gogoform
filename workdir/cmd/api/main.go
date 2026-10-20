// Command api starts the HTTP API server.
//
//	@title			App API
//	@version		1.0
//	@description	REST API of the application. Tenant-scoped endpoints also accept the session cookie of the /app pages; state-changing requests authenticated by it must come from the same origin.
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
	"strings"
	"syscall"
	"time"

	"app/cmd/api/handlers"
	"app/docs"
	"app/internal/config"
	"app/internal/database"
	"app/internal/db"
	"app/internal/logger"
	"app/internal/pkg/auth"
	"app/internal/pkg/billing"
	"app/internal/pkg/mailer"
	"app/internal/pkg/tenants"
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

	pool, err := database.Connect(ctx, cfg.Database.URI)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Info("database connected",
		"host", pool.Config().ConnConfig.Host, "port", pool.Config().ConnConfig.Port, "database", pool.Config().ConnConfig.Database)

	queries := db.New(pool)
	txRunner := database.NewTxRunner(pool)
	mail := newMailer(log, cfg.Mail, cfg.Server.Env)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mail.Close(closeCtx); err != nil {
			log.Warn("mailer: did not drain in time", "error", err)
		}
	}()
	authSvc, err := newAuthService(ctx, log, queries, txRunner, cfg.Auth, mail, cfg.App.BaseURL, cfg.Server.Env)
	if err != nil {
		return err
	}
	billingSvc, err := newBillingService(log, queries, txRunner, cfg.Stripe, mail, cfg.App.BaseURL)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	docs.SwaggerInfo.Host = fmt.Sprintf("localhost:%d", cfg.Server.Port)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handlers.Routes(handlers.NewServices(queries, authSvc, billingSvc, mail), cfg.Features, cfg.Stripe, cfg.App, cfg.Security),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
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

// newMailer returns the SMTP mailer wrapped in mailer.Async when cfg is
// enabled, or a log-only mailer otherwise (with a warning outside
// development, where unconfigured email is most likely a mistake).
func newMailer(log *slog.Logger, cfg config.MailConfig, env string) *mailer.Async {
	var next mailer.Mailer
	if cfg.Enabled() {
		smtp, err := mailer.NewSMTPMailer(mailer.SMTPConfig{
			Host: cfg.Host, Port: cfg.Port, User: cfg.User, Password: cfg.Password, From: cfg.From,
		})
		if err != nil {
			log.Error("mailer: invalid SMTP configuration; falling back to logging email", "error", err)
			next = mailer.NewLogMailer(log)
		} else {
			log.Info("mailer: sending email through SMTP", "host", cfg.Host, "port", cfg.Port)
			next = smtp
		}
	} else {
		if env != "development" {
			log.Warn("mailer: SMTP is not configured; email is only logged, not delivered")
		}
		next = mailer.NewLogMailer(log)
	}
	return mailer.NewAsync(next, log, mailer.AsyncOptions{})
}

// newAuthService builds the user authentication service and creates the
// bootstrap admin of cfg, if any. tx runs the writes that must be atomic
// (creating a user together with its tenant). Config.validate already
// refuses to load a configuration without a secret outside development, so
// the random-secret fallback below only ever fires in development, where
// tokens not surviving a restart is an acceptable trade-off; the env check
// here is a defensive backstop in case that invariant is ever bypassed. mail
// sends the email verification and password reset links, built from baseURL.
func newAuthService(ctx context.Context, log *slog.Logger, q db.Querier, tx database.TxRunner, cfg config.AuthConfig, mail mailer.Mailer, baseURL, env string) (*auth.Service, error) {
	secret := []byte(cfg.JWTSecret)
	if len(secret) == 0 {
		if !strings.EqualFold(env, "development") {
			return nil, fmt.Errorf("auth: AUTH_JWT_SECRET must be set outside development")
		}
		log.Warn("auth: AUTH_JWT_SECRET is not set; using a random secret, so tokens are invalidated on restart")
		secret = make([]byte, auth.MinSecretLen)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("auth: generate secret: %w", err)
		}
	}
	svc, err := auth.NewService(auth.NewUserStore(q), auth.NewUserTokenStore(q), auth.Options{
		Secret: secret, TokenTTL: cfg.TokenTTL(), Tx: tx, Notifier: auth.NewMailNotifier(mail, baseURL),
	})
	if err != nil {
		return nil, err
	}
	if cfg.AdminUsername != "" {
		created, err := svc.EnsureUser(ctx, cfg.AdminUsername, "", cfg.AdminPassword, auth.RoleAdmin)
		if err != nil {
			return nil, fmt.Errorf("auth: bootstrap admin: %w", err)
		}
		if created {
			log.Info("auth: bootstrap admin created", "username", auth.NormalizeUsername(cfg.AdminUsername))
		}
	}
	return svc, nil
}

// newBillingService builds the Stripe billing service, or returns nil when
// Stripe is not configured, which leaves the billing routes unmounted. mail
// sends the payment-failed email, built from baseURL.
func newBillingService(log *slog.Logger, q db.Querier, tx database.TxRunner, cfg config.StripeConfig, mail mailer.Mailer, baseURL string) (*billing.Service, error) {
	if !cfg.Enabled() {
		log.Info("billing: Stripe is not configured; billing routes are disabled")
		return nil, nil
	}
	svc, err := billing.NewService(billing.NewStripeGateway(cfg.SecretKey), tenants.NewService(q), billing.Options{
		PriceID:  cfg.PriceID,
		Tx:       tx,
		Notifier: billing.NewMailNotifier(mail, baseURL),
	})
	if err != nil {
		return nil, fmt.Errorf("billing: %w", err)
	}
	return svc, nil
}
