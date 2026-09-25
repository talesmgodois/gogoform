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
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	"app/docs"
	"app/internal/config"
	"app/internal/database"
	"app/internal/db"
	"app/internal/handler"
	"app/internal/logger"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
	"app/internal/pkg/tenants"
	"app/internal/pkg/webhooks"
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

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	docs.SwaggerInfo.Host = fmt.Sprintf("localhost:%d", cfg.Server.Port)

	srv := &http.Server{
		Addr:              addr,
		Handler:           routes(newServices(db.New(pool)), cfg.App),
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

// services holds the domain repositories the controllers are built on.
type services struct {
	tenants     tenants.Repository
	forms       forms.Repository
	submissions submissions.Repository
	webhooks    webhooks.Repository
	files       files.Repository
}

// newServices returns the sqlc-backed implementation of every repository.
func newServices(q db.Querier) services {
	return services{
		tenants:     tenants.NewService(q),
		forms:       forms.NewService(q),
		submissions: submissions.NewService(q),
		webhooks:    webhooks.NewService(q),
		files:       files.NewService(q),
	}
}

// routes builds the HTTP handler. The /app dashboard is only mounted when
// appCfg holds credentials, because it lists every tenant's data.
func routes(svc services, appCfg config.AppConfig) http.Handler {
	tenantsCtl := &tenantsController{tenants: svc.tenants}
	formsCtl := &formsController{forms: svc.forms}
	submissionsCtl := &submissionsController{forms: svc.forms, submissions: svc.submissions}
	webhooksCtl := &webhooksController{forms: svc.forms, webhooks: svc.webhooks}
	filesCtl := &filesController{files: svc.files}
	auth := tenantsCtl.Authenticate

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.Health)
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	mux.HandleFunc("POST /tenants", tenantsCtl.Create)
	mux.HandleFunc("GET /tenants/me", auth(tenantsCtl.Me))

	mux.HandleFunc("POST /forms", auth(formsCtl.Create))
	mux.HandleFunc("GET /forms", auth(formsCtl.List))
	mux.HandleFunc("GET /forms/{id}", auth(formsCtl.Get))
	mux.HandleFunc("PUT /forms/{id}", auth(formsCtl.Update))
	mux.HandleFunc("DELETE /forms/{id}", auth(formsCtl.Delete))

	mux.HandleFunc("GET /forms/{id}/submissions", auth(submissionsCtl.List))

	mux.HandleFunc("POST /forms/{id}/webhooks", auth(webhooksCtl.Create))
	mux.HandleFunc("GET /forms/{id}/webhooks", auth(webhooksCtl.List))

	mux.HandleFunc("POST /files", auth(filesCtl.Create))
	mux.HandleFunc("GET /files/{id}", filesCtl.Get)
	mux.HandleFunc("DELETE /files/{id}", auth(filesCtl.Delete))

	mux.HandleFunc("GET /public/forms/{slug}", formsCtl.GetPublic)
	mux.HandleFunc("POST /public/forms/{slug}/submissions", submissionsCtl.Create)

	if appCfg.Enabled() {
		appCtl := &appController{forms: svc.forms, files: svc.files}
		mux.HandleFunc("GET /app", basicAuth(appCfg, appCtl.Index))
		mux.Handle("GET /app/{$}", http.RedirectHandler("/app", http.StatusMovedPermanently))
	}
	return mux
}
