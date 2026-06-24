package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/worker"
)

type Enqueuer interface {
	Enqueue()
}

type Bot struct {
	cfg    *config.Config
	api    *tgbotapi.BotAPI
	jobs   repo.JobRepo
	tokens repo.TokenRepo
	files  repo.FileRepo
	pool   Enqueuer
}

func New(cfg *config.Config, jobs repo.JobRepo, files repo.FileRepo, tokens repo.TokenRepo, pool Enqueuer) (*Bot, error) {
	var httpClient *http.Client
	if cfg.TelegramProxy != "" {
		proxyURL, err := url.Parse(cfg.TelegramProxy)
		if err != nil {
			return nil, fmt.Errorf("parse telegram proxy: %w", err)
		}
		httpClient = &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		}
	}

	var api *tgbotapi.BotAPI
	var err error
	if httpClient != nil {
		api, err = tgbotapi.NewBotAPIWithClient(cfg.TelegramBotToken, tgbotapi.APIEndpoint, httpClient)
	} else {
		api, err = tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	}
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}
	api.Debug = cfg.TelegramDebug

	slog.Info("bot: authorized", "username", api.Self.UserName)

	b := &Bot{cfg: cfg, api: api, jobs: jobs, files: files, tokens: tokens, pool: pool}
	b.setCommands()
	return b, nil
}

// setCommands регистрирует команды в меню Telegram-бота.
func (b *Bot) setCommands() {
	cmds := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Начало работы"},
		tgbotapi.BotCommand{Command: "status", Description: "Статус очереди"},
		tgbotapi.BotCommand{Command: "queue", Description: "Активные задачи"},
		tgbotapi.BotCommand{Command: "last", Description: "Последние файлы (/last N, по умолчанию 5)"},
		tgbotapi.BotCommand{Command: "search", Description: "Поиск по файлам (/search запрос)"},
		tgbotapi.BotCommand{Command: "help", Description: "Помощь"},
	)
	if _, err := b.api.Request(cmds); err != nil {
		slog.Warn("bot: set commands", "err", err)
	}
}

// Notify реализует worker.Notifier — отправляет сообщение с inline-кнопками.
// Используем только callback-кнопки: Telegram отклоняет URL-кнопки с localhost
// и LAN-адресами, а бот строит URL самостоятельно из cfg.BaseURL + token.
func (b *Bot) Notify(ctx context.Context, n worker.Notification) {
	msg := tgbotapi.NewMessage(n.ChatID, n.Text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true

	var rows [][]tgbotapi.InlineKeyboardButton
	if !n.IsFailure && n.Token != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶️ Смотреть", "view:"+n.Token),
			tgbotapi.NewInlineKeyboardButtonData("📥 Скачать", "dl:"+n.Token),
		))
	}
	if n.IsFailure && n.JobID != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("↩ Повторить", "retry:"+n.JobID),
		))
	}
	if len(rows) > 0 {
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}

	if _, err := b.api.Send(msg); err != nil {
		slog.Error("bot: notify", "chat_id", n.ChatID, "err", err)
	}
}

// Start запускает long polling и блокируется до отмены ctx.
func (b *Bot) Start(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 20 // короче NAT-таймаута типичных сред (OrbStack, Docker NAT ~25s)

	updates := b.api.GetUpdatesChan(u)
	slog.Info("bot: started polling")

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			slog.Info("bot: stopped")
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.Message != nil {
				go b.handleMessage(ctx, update.Message)
			} else if update.CallbackQuery != nil {
				go b.handleCallback(ctx, update.CallbackQuery)
			}
		}
	}
}

func (b *Bot) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("bot: send", "err", err)
	}
}

func (b *Bot) answerCallback(queryID, text string) {
	cb := tgbotapi.NewCallback(queryID, text)
	if _, err := b.api.Request(cb); err != nil {
		slog.Error("bot: answer callback", "err", err)
	}
}
