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

	"app/cmd/api/handlers"
	"app/docs"
	"app/internal/config"
	"app/internal/database"
	"app/internal/db"
	"app/internal/logger"
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
		Handler:           handlers.Routes(handlers.NewServices(db.New(pool)), cfg.App),
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
