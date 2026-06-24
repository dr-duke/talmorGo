package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAllowed(msg.Chat.ID) {
		b.send(msg.Chat.ID, "🛑 This bot is private")
		return
	}
	if msg.IsCommand() {
		b.handleCommand(ctx, msg)
	} else {
		b.handleURL(ctx, msg)
	}
}

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	switch msg.Command() {
	case "start":
		b.send(msg.Chat.ID,
			"🎬 <b>TalmorGo</b>\n\nОтправь ссылку на видео — скачаю и положу в библиотеку.\n"+
				"По завершении получишь уведомление с кнопками для просмотра и скачивания.")
	case "help":
		b.send(msg.Chat.ID,
			"📋 <b>Команды:</b>\n"+
				"/status — статус очереди\n"+
				"/queue — активные задачи\n"+
				"/last [N] — последние N файлов (по умолчанию 5)\n"+
				"/search запрос — поиск по файлам, URL и тегам\n\n"+
				"Просто отправь ссылку, чтобы поставить в очередь.\n"+
				"Можно отправить несколько ссылок через пробел.")
	case "status":
		b.handleStatus(ctx, msg.Chat.ID)
	case "queue":
		b.handleQueue(ctx, msg.Chat.ID)
	case "search":
		b.handleSearch(ctx, msg.Chat.ID, msg.CommandArguments())
	case "last":
		b.handleLast(ctx, msg.Chat.ID, msg.CommandArguments())
	default:
		b.send(msg.Chat.ID, "Неизвестная команда. Отправь /help")
	}
}

func (b *Bot) handleStatus(ctx context.Context, chatID int64) {
	all, err := b.jobs.List(ctx, repo.JobFilter{})
	if err != nil {
		b.send(chatID, "Ошибка получения статуса")
		return
	}
	counts := map[model.JobStatus]int{}
	for _, j := range all {
		counts[j.Status]++
	}
	b.send(chatID, fmt.Sprintf(
		"📊 <b>Статус очереди:</b>\n⏳ Ожидание: %d\n▶️ В работе: %d\n🔄 Повтор: %d\n✅ Готово: %d\n❌ Ошибка: %d",
		counts[model.JobPending], counts[model.JobRunning], counts[model.JobRetrying],
		counts[model.JobDone], counts[model.JobFailed],
	))
}

func (b *Bot) handleQueue(ctx context.Context, chatID int64) {
	jobs, err := b.jobs.List(ctx, repo.JobFilter{
		Statuses: []model.JobStatus{model.JobPending, model.JobRunning, model.JobRetrying},
	})
	if err != nil {
		b.send(chatID, "Ошибка получения очереди")
		return
	}
	if len(jobs) == 0 {
		b.send(chatID, "Очередь пуста")
		return
	}
	var sb strings.Builder
	sb.WriteString("📋 <b>Активные задачи:</b>\n")
	for _, j := range jobs {
		status := "⏳"
		switch j.Status {
		case model.JobRunning:
			status = "▶️"
		case model.JobRetrying:
			status = "🔄"
		}
		shortID := j.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		name := j.URL
		if j.Title != "" {
			name = j.Title
		}
		sb.WriteString(fmt.Sprintf("%s <code>%s</code> %s\n", status, shortID, shortenURL(name)))
	}
	b.send(chatID, sb.String())
}

func (b *Bot) handleURL(ctx context.Context, msg *tgbotapi.Message) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	var added, invalid int
	for _, part := range strings.Fields(text) {
		if _, err := url.ParseRequestURI(part); err != nil {
			invalid++
			continue
		}
		job := &model.Job{
			URL:    part,
			Status: model.JobPending,
			Source: "telegram",
			ChatID: msg.Chat.ID,
		}
		if err := b.jobs.Create(ctx, job); err != nil {
			slog.Error("bot: create job", "err", err)
			continue
		}
		b.pool.Enqueue()
		added++
	}

	switch {
	case added > 0 && invalid == 0:
		b.send(msg.Chat.ID, fmt.Sprintf("✅ Добавлено в очередь: %d", added))
	case added > 0:
		b.send(msg.Chat.ID, fmt.Sprintf("✅ Добавлено: %d, пропущено (не URL): %d", added, invalid))
	default:
		b.send(msg.Chat.ID, "❌ Не найдено корректных ссылок")
	}
}

// handleCallback обрабатывает нажатие inline-кнопок.
func (b *Bot) handleCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	if !b.isAllowed(cq.From.ID) {
		b.answerCallback(cq.ID, "🛑 Доступ запрещён")
		return
	}

	data := cq.Data
	chatID := cq.Message.Chat.ID

	switch {
	case strings.HasPrefix(data, "view:"):
		// Смотреть — ссылка на потоковый просмотр.
		token := strings.TrimPrefix(data, "view:")
		b.send(chatID, "▶️ "+b.cfg.BaseURL+"/f/"+token)
		b.answerCallback(cq.ID, "Ссылка для просмотра отправлена")

	case strings.HasPrefix(data, "dl:"):
		// Скачать — ссылка со скачиванием файла.
		token := strings.TrimPrefix(data, "dl:")
		b.send(chatID, "📥 "+b.cfg.BaseURL+"/f/"+token+"?download=true")
		b.answerCallback(cq.ID, "Ссылка для скачивания отправлена")

	case strings.HasPrefix(data, "link:"):
		// Постоянная ссылка (устаревший вариант, сохранён для обратной совместимости).
		token := strings.TrimPrefix(data, "link:")
		b.send(chatID, "🔗 "+b.cfg.BaseURL+"/f/"+token)
		b.answerCallback(cq.ID, "Ссылка отправлена")

	case strings.HasPrefix(data, "retry:"):
		// Сбрасываем failed-задачу в pending.
		jobID := strings.TrimPrefix(data, "retry:")
		if err := b.jobs.ResetFailed(ctx, jobID); err != nil {
			b.answerCallback(cq.ID, "Ошибка: "+err.Error())
			return
		}
		b.pool.Enqueue()
		b.answerCallback(cq.ID, "Добавлено в очередь")
		// Редактируем сообщение, чтобы убрать кнопку "Повторить".
		edit := tgbotapi.NewEditMessageText(chatID, cq.Message.MessageID,
			cq.Message.Text+"\n\n⏳ Повторная попытка добавлена в очередь")
		edit.ParseMode = tgbotapi.ModeHTML
		b.api.Send(edit) //nolint:errcheck

	default:
		b.answerCallback(cq.ID, "")
	}
}

func (b *Bot) handleSearch(ctx context.Context, chatID int64, args string) {
	q := strings.TrimSpace(args)
	if q == "" {
		b.send(chatID, "Использование: /search <запрос>")
		return
	}
	items, err := b.jobs.SearchMedia(ctx, q)
	if err != nil {
		b.send(chatID, "Ошибка поиска")
		return
	}
	if len(items) == 0 {
		b.send(chatID, fmt.Sprintf("🔍 По запросу «%s» ничего не найдено", q))
		return
	}
	b.sendMediaList(ctx, chatID,
		fmt.Sprintf("🔍 По запросу «%s» найдено %d:", q, len(items)),
		items)
}

func (b *Bot) handleLast(ctx context.Context, chatID int64, args string) {
	n := 5
	if s := strings.TrimSpace(args); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	if n > 20 {
		n = 20
	}
	items, err := b.jobs.LastMedia(ctx, n)
	if err != nil {
		b.send(chatID, "Ошибка получения списка")
		return
	}
	if len(items) == 0 {
		b.send(chatID, "Скачанных файлов пока нет")
		return
	}
	b.sendMediaList(ctx, chatID,
		fmt.Sprintf("📼 Последние %d файлов:", len(items)),
		items)
}

// sendMediaList отправляет нумерованный список с inline-кнопками для каждого доступного файла.
func (b *Bot) sendMediaList(ctx context.Context, chatID int64, header string, items []*model.MediaItem) {
	var sb strings.Builder
	sb.WriteString(header + "\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, item := range items {
		title := shortenURL(item.DisplayTitle())
		tags := ""
		if len(item.Tags) > 0 {
			tags = " · 🏷 " + strings.Join(item.Tags, ", ")
		}
		domain := item.Job.Domain()
		sb.WriteString(fmt.Sprintf("\n%d. <b>%s</b>\n   🌐 %s%s\n",
			i+1, escapeHTML(title), domain, tags))

		if item.File == nil || !item.File.IsAvailable() {
			continue
		}
		tok, err := b.tokens.Upsert(ctx, item.File.ID)
		if err != nil {
			slog.Error("bot: upsert token", "file_id", item.File.ID, "err", err)
			continue
		}
		label := fmt.Sprintf("%d. %s", i+1, truncate(item.DisplayTitle(), 18))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶️ "+label, "view:"+tok.Token),
			tgbotapi.NewInlineKeyboardButtonData("📥", "dl:"+tok.Token),
		))
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	if len(rows) > 0 {
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("bot: send media list", "err", err)
	}
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func (b *Bot) isAllowed(chatID int64) bool {
	if len(b.cfg.TelegramAllowedIDs) == 0 {
		slog.Warn("bot: TELEGRAM_ALLOWED_IDS not set — all users allowed")
		return true
	}
	return slices.Contains(b.cfg.TelegramAllowedIDs, chatID)
}

func shortenURL(u string) string {
	if len(u) > 45 {
		return u[:42] + "…"
	}
	return u
}
