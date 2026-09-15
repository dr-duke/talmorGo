package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/queue"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/worker"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// messenger — часть Telegram API, которой пользуется бот при отправке.
// Интерфейс позволяет проверять разбор команд и сценарии сообщений без сети.
type messenger interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

type Bot struct {
	cfg    *config.Config
	api    messenger
	poller *tgbotapi.BotAPI // источник обновлений; в тестах не нужен
	jobs   repo.JobRepo
	tokens repo.TokenRepo
	queue  *queue.Service

	// pendingMsgs хранит id сообщения «в очереди» по id задания-заготовки:
	// если ссылка окажется плейлистом, заготовка удаляется вместе с job_id,
	// и заменить сообщение сводкой можно только по этой связке.
	mu          sync.Mutex
	pendingMsgs map[string]int64
}

func New(cfg *config.Config, jobs repo.JobRepo, tokens repo.TokenRepo, q *queue.Service) (*Bot, error) {
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

	// Предупреждаем один раз при старте, а не на каждое сообщение: в прежнем
	// виде строка тонула в потоке логов именно тогда, когда была важнее всего.
	if len(cfg.TelegramAllowedIDs) == 0 {
		slog.Warn("bot: TELEGRAM_ALLOWED_IDS не задан — бот принимает ссылки от любого, " +
			"кто узнает его имя")
	}

	b := &Bot{
		cfg: cfg, api: api, poller: api, jobs: jobs, tokens: tokens, queue: q,
		pendingMsgs: make(map[string]int64),
	}
	b.setCommands()
	return b, nil
}

// rememberPending связывает задание-заготовку с отправленным сообщением.
func (b *Bot) rememberPending(jobID string, msgID int64) {
	b.mu.Lock()
	b.pendingMsgs[jobID] = msgID
	b.mu.Unlock()
}

// takePending возвращает и забывает id сообщения для задания-заготовки.
func (b *Bot) takePending(jobID string) (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msgID, ok := b.pendingMsgs[jobID]
	delete(b.pendingMsgs, jobID)
	return msgID, ok
}

func (b *Bot) setCommands() {
	cmds := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Начало работы"},
		tgbotapi.BotCommand{Command: "status", Description: "Статус очереди"},
		tgbotapi.BotCommand{Command: "queue", Description: "Активные задачи"},
		tgbotapi.BotCommand{Command: "last", Description: "Последние файлы (/last N, по умолчанию 5)"},
		tgbotapi.BotCommand{Command: "search", Description: "Поиск по файлам (/search запрос)"},
		tgbotapi.BotCommand{Command: "web", Description: "Перейти на сайт"},
		tgbotapi.BotCommand{Command: "help", Description: "Помощь"},
	)
	if _, err := b.api.Request(cmds); err != nil {
		slog.Warn("bot: set commands", "err", err)
	}
}

// Notify реализует worker.Notifier.
func (b *Bot) Notify(ctx context.Context, n worker.Notification) {
	switch n.Kind {

	case worker.NotifJobStarted:
		// Редактируем сообщение очереди → «скачивается».
		if n.MessageID == 0 {
			return
		}
		b.editMsg(n.ChatID, int(n.MessageID),
			"⬇️ <b>Скачивается…</b>\n"+escapeHTML(shortenMsg(n.JobURL)),
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🛑 Отменить", "stop:"+n.JobID),
				),
			),
		)

	case worker.NotifFileDone:
		// Новое сообщение-карточка на каждый файл.
		b.sendFileCard(n.ChatID, n.FileName, n.Token)

	case worker.NotifJobDone:
		// Удаляем сообщение очереди — карточки уже появились выше.
		if n.MessageID != 0 {
			b.deleteMsg(n.ChatID, int(n.MessageID))
		}

	case worker.NotifJobFailed:
		// Правим сообщение очереди → «ошибка», а при его отсутствии отправляем
		// новое (см. editOrSend).
		errShort := n.ErrText
		if len([]rune(errShort)) > 200 {
			errShort = string([]rune(errShort)[:197]) + "…"
		}
		b.editOrSend(ctx, n,
			"❌ <b>Ошибка скачивания</b>\n"+escapeHTML(shortenMsg(n.JobURL))+"\n\n<code>"+escapeHTML(errShort)+"</code>",
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("↩ Повторить", "retry:"+n.JobID),
				),
			),
		)

	case worker.NotifJobRetrying:
		// Правим сообщение очереди → «повтор через…», а при его отсутствии
		// отправляем новое.
		b.editOrSend(ctx, n,
			"🔄 <b>Повтор "+n.RetryAt+"</b>\n"+escapeHTML(shortenMsg(n.JobURL)),
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🛑 Отменить", "stop:"+n.JobID),
				),
			),
		)
	}
}

// editOrSend правит сообщение задания, а при его отсутствии отправляет новое и
// запоминает id.
//
// У заданий, порождённых из плейлиста, TgMessageID не заполняется: CreateJobs
// проставляет только источник и чат. Из-за этого ветки уведомлений выходили
// раньше времени, и о недоступных роликах пользователь не узнавал вовсе —
// приходили карточки уцелевших, а по упавшим ни сообщения об ошибке, ни кнопки
// «Повторить»; увидеть их можно было только через веб.
//
// Id сохраняется, чтобы следующие уведомления по тому же заданию правили это
// сообщение, а не сыпали новыми: повторов у задания бывает много.
func (b *Bot) editOrSend(ctx context.Context, n worker.Notification, text string, kb tgbotapi.InlineKeyboardMarkup) {
	if n.MessageID != 0 {
		b.editMsg(n.ChatID, int(n.MessageID), text, kb)
		return
	}
	if n.ChatID == 0 {
		return
	}
	msgID := b.sendMarkup(n.ChatID, text, &kb)
	if msgID != 0 && n.JobID != "" {
		if err := b.jobs.SetTgMessageID(ctx, n.JobID, msgID); err != nil {
			slog.Warn("bot: сохранение id сообщения", "job", n.JobID, "err", err)
		}
	}
}

// sendFileCard отправляет карточку скачанного файла.
// При публичном BASE_URL — URL-кнопки (прямое открытие/скачивание).
// При localhost/private — callback-кнопки (бот присылает ссылку текстом).
func (b *Bot) sendFileCard(chatID int64, name, token string) {
	text := "✅ <b>" + escapeHTML(name) + "</b>"
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true

	if b.isPublic() {
		viewURL := b.cfg.LinkBase() + "/f/" + token
		dlURL := b.cfg.LinkBase() + "/f/" + token + "?download=true"
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonURL("▶️ Смотреть", viewURL),
				tgbotapi.NewInlineKeyboardButtonURL("📥 Скачать", dlURL),
			),
		)
	} else {
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("▶️ Смотреть", "view:"+token),
				tgbotapi.NewInlineKeyboardButtonData("📥 Скачать", "dl:"+token),
			),
		)
	}
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("bot: send file card", "err", err)
	}
}

// isPublic возвращает true если BASE_URL указывает на публичный домен
// (не localhost и не RFC-1918 private range).
func (b *Bot) isPublic() bool {
	if b.cfg.BaseURL == "" {
		return false
	}
	parsed, err := url.Parse(b.cfg.BaseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}
	if strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "192.168.") {
		return false
	}
	// 172.16.0.0/12 → 172.16.x.x – 172.31.x.x
	if strings.HasPrefix(host, "172.") {
		parts := strings.SplitN(host, ".", 3)
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n >= 16 && n <= 31 {
				return false
			}
		}
	}
	return true
}

// Start запускает long polling и блокируется до отмены ctx.
func (b *Bot) Start(ctx context.Context) {
	if b.poller == nil {
		return
	}
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 20

	updates := b.poller.GetUpdatesChan(u)
	slog.Info("bot: started polling")

	for {
		select {
		case <-ctx.Done():
			b.poller.StopReceivingUpdates()
			slog.Info("bot: stopped")
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			// Обработчики идут в горутинах, поэтому паника в любом из них
			// уносила весь процесс — вместе с веб-интерфейсом, пулом загрузок
			// и активными скачиваниями. Ошибка одного сообщения не должна
			// стоить работающих загрузок.
			if update.Message != nil {
				msg := update.Message
				go func() {
					defer recoverUpdate("message")
					b.handleMessage(ctx, msg)
				}()
			} else if update.CallbackQuery != nil {
				cq := update.CallbackQuery
				go func() {
					defer recoverUpdate("callback")
					b.handleCallback(ctx, cq)
				}()
			}
		}
	}
}

// recoverUpdate гасит панику обработчика обновления.
func recoverUpdate(kind string) {
	if r := recover(); r != nil {
		slog.Error("bot: паника в обработчике обновления",
			"kind", kind, "panic", r, "stack", string(debug.Stack()))
	}
}

// ── низкоуровневые методы ──────────────────────────────────────────────────

// send отправляет текстовое сообщение, возвращает message_id (0 при ошибке).
func (b *Bot) send(chatID int64, text string) {
	b.sendMarkup(chatID, text, nil)
}

func (b *Bot) sendMarkup(chatID int64, text string, markup *tgbotapi.InlineKeyboardMarkup) int64 {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	if markup != nil {
		msg.ReplyMarkup = *markup
	}
	sent, err := b.api.Send(msg)
	if err != nil {
		slog.Error("bot: send", "err", err)
		return 0
	}
	return int64(sent.MessageID)
}

func (b *Bot) editMsg(chatID int64, msgID int, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	edit.ReplyMarkup = &keyboard
	if _, err := b.api.Send(edit); err != nil {
		slog.Debug("bot: edit message", "err", err)
	}
}

func (b *Bot) deleteMsg(chatID int64, msgID int) {
	del := tgbotapi.NewDeleteMessage(chatID, msgID)
	if _, err := b.api.Request(del); err != nil {
		slog.Debug("bot: delete message", "err", err)
	}
}

func (b *Bot) answerCallback(queryID, text string) {
	cb := tgbotapi.NewCallback(queryID, text)
	if _, err := b.api.Request(cb); err != nil {
		slog.Error("bot: answer callback", "err", err)
	}
}

func shortenMsg(s string) string {
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:57]) + "…"
	}
	return s
}
