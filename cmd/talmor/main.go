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
	"github.com/dr-duke/talmorGo/internal/library"
	"github.com/dr-duke/talmorGo/internal/ops"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/queue"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
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

	// Пустой WEB_TOKEN полностью отключает проверку доступа — и раньше делал
	// это молча. Для TELEGRAM_ALLOWED_IDS предупреждение спецификацией
	// предусмотрено, а для веб-пароля не было ни в коде, ни в документации:
	// выкатив экземпляр за Ingress и забыв переменную, человек получал
	// открытый интерфейс с удалением файлов и настройками, не узнав об этом.
	if cfg.WebToken == "" {
		slog.Warn("WEB_TOKEN не задан — веб-интерфейс открыт без авторизации: " +
			"доступны удаление файлов, постоянные ссылки и настройки")
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Error("db open", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer database.Close()
	slog.Info("db opened", "path", cfg.DBPath)

	// ── Репозитории ──
	jobRepo := repo.NewJobRepo(database)
	itemRepo := repo.NewItemRepo(database)
	tokenRepo := repo.NewTokenRepo(database)
	tagRepo := repo.NewTagRepo(database)
	cookieRepo := repo.NewCookieRepo(database)
	settingsRepo := repo.NewSettingsRepo(database)
	collectionRepo := repo.NewCollectionRepo(database)
	operationRepo := repo.NewOperationRepo(database)

	// ── Инфраструктура ──
	hub := sse.New()
	store := storage.New(cfg.YtDlpOutputDir)
	settingsProvider := settings.New(cfg, settingsRepo)

	// ── Исполнители ──
	pool := worker.NewPool(cfg, jobRepo, itemRepo, tokenRepo, nil)
	pool.SetHub(hub)
	pool.SetSettings(settingsProvider)

	opsWorker := ops.NewWorker(operationRepo, tagRepo, jobRepo, itemRepo, store, cfg, hub)

	// ── Сервисы ──
	libSvc := &library.Service{
		Jobs: jobRepo, Items: itemRepo, Tags: tagRepo, Tokens: tokenRepo,
		Collections: collectionRepo, Ops: operationRepo,
		Storage: store, Settings: settingsProvider, Cfg: cfg, Runner: opsWorker,
	}

	expander := playlist.New(jobRepo, tagRepo)
	expander.Hub = hub
	queueSvc := &queue.Service{
		Jobs: jobRepo, Items: itemRepo, Storage: store,
		Expander: expander, Pool: pool, Settings: settingsProvider, Hub: hub,
	}

	// ── Telegram ──
	var tgBot *bot.Bot
	if cfg.TelegramBotToken != "" {
		tgBot, err = bot.New(cfg, jobRepo, tokenRepo, queueSvc)
		if err != nil {
			slog.Warn("bot init failed, running without telegram", "err", err)
		} else {
			pool.SetNotifier(tgBot)
			queueSvc.SetObserver(tgBot)
		}
	} else {
		slog.Info("TELEGRAM_BOT_TOKEN not set, running in web-only mode")
	}

	// ── HTTP ──
	srv := api.New(api.Deps{
		Cfg: cfg, Lib: libSvc, Queue: queueSvc,
		Settings: settingsProvider, Cookies: cookieRepo, Hub: hub,
	})
	httpServer := newHTTPServer(cfg.HTTPHost+":"+cfg.HTTPPort, srv.Handler())

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

	// Задания, оборванные на проверке плейлиста, разворачиваем заново.
	queueSvc.RecoverChecking(ctx)

	<-ctx.Done()
	slog.Info("shutting down…")
	httpServer.Shutdown(context.Background()) //nolint:errcheck
}
