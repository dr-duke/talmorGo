package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dr-duke/talmorGo/internal/api"
	"github.com/dr-duke/talmorGo/internal/bot"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/dr-duke/talmorGo/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if cfg.TelegramDebug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Error("db open", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer database.Close()
	slog.Info("db opened", "path", cfg.DBPath)

	jobRepo := repo.NewJobRepo(database)
	fileRepo := repo.NewFileRepo(database)
	tokenRepo := repo.NewTokenRepo(database)
	tagRepo := repo.NewTagRepo(database)

	pool := worker.NewPool(cfg, jobRepo, fileRepo, tokenRepo, nil)

	tgBot, err := bot.New(cfg, jobRepo, fileRepo, tokenRepo, pool)
	if err != nil {
		slog.Error("bot init", "err", err)
		os.Exit(1)
	}
	pool.SetNotifier(tgBot)

	store := storage.New(cfg.YtDlpOutputDir)
	srv := api.New(cfg, jobRepo, fileRepo, tokenRepo, tagRepo, store, pool)
	httpServer := &http.Server{
		Addr:    cfg.HTTPHost + ":" + cfg.HTTPPort,
		Handler: srv.Handler(),
	}

	checker := worker.NewFileChecker(fileRepo, cfg.FileCheckInterval)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		slog.Info("http: listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server", "err", err)
		}
	}()

	go pool.Start(ctx)
	go tgBot.Start(ctx)
	go checker.Start(ctx)

	<-ctx.Done()
	slog.Info("shutting down…")
	httpServer.Shutdown(context.Background()) //nolint:errcheck
}
