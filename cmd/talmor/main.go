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
	"github.com/dr-duke/talmorGo/internal/ops"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
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
	itemRepo := repo.NewItemRepo(database)
	tokenRepo := repo.NewTokenRepo(database)
	tagRepo := repo.NewTagRepo(database)
	cookieRepo := repo.NewCookieRepo(database)
	settingsRepo := repo.NewSettingsRepo(database)
	collectionRepo := repo.NewCollectionRepo(database)
	operationRepo := repo.NewOperationRepo(database)

	hub := sse.New()

	pool := worker.NewPool(cfg, jobRepo, itemRepo, tokenRepo, nil)
	pool.SetHub(hub)
	pool.SetSettingsRepo(settingsRepo)

	opsWorker := ops.NewWorker(operationRepo, tagRepo, jobRepo, itemRepo, cfg, hub)

	var tgBot *bot.Bot
	if cfg.TelegramBotToken != "" {
		tgBot, err = bot.New(cfg, jobRepo, itemRepo, tokenRepo, tagRepo, pool)
		if err != nil {
			slog.Warn("bot init failed, running without telegram", "err", err)
		} else {
			pool.SetNotifier(tgBot)
		}
	} else {
		slog.Info("TELEGRAM_BOT_TOKEN not set, running in web-only mode")
	}

	store := storage.New(cfg.YtDlpOutputDir)
	srv := api.New(cfg, jobRepo, itemRepo, tokenRepo, tagRepo, cookieRepo, settingsRepo, collectionRepo, operationRepo, store, pool, opsWorker, hub)
	httpServer := &http.Server{
		Addr:    cfg.HTTPHost + ":" + cfg.HTTPPort,
		Handler: srv.Handler(),
	}

	checker := worker.NewFileChecker(itemRepo, cfg.FileCheckInterval)
	dirScanner := worker.NewDirScanner(jobRepo, itemRepo, cfg.YtDlpOutputDir, cfg.DirScanInterval, pool.InFlight())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		slog.Info("http: listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server", "err", err)
		}
	}()

	go pool.Start(ctx)
	go opsWorker.Start(ctx)
	if tgBot != nil {
		go tgBot.Start(ctx)
	}
	go checker.Start(ctx)
	go dirScanner.Start(ctx)

	<-ctx.Done()
	slog.Info("shutting down…")
	httpServer.Shutdown(context.Background()) //nolint:errcheck
}
