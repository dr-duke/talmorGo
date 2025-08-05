package main

import (
	"encoding/json"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jessevdk/go-flags"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

type TelegramBotConfig struct {
	BotToken             string  `long:"telegram-bot-token" env:"TELEGRAM_BOT_TOKEN" required:"true"`
	BotWorkerCount       int     `long:"worker-count" env:"WORKER_COUNT" default:"5"`
	AllowedUserIDs       []int64 `long:"allowed-chatids" env:"ALLOWED_IDS" env-delim:";"`
	Debug                bool    `long:"bot-debug"`
	EnableWebPagePreview bool    `long:"bot-web-preview"`
	HttpPort             string  `long:"http-port" default:"8080"`
	HealthEndpoint       string  `long:"health-endpoint" default:"/health"`
	httpMux              *http.ServeMux
	bot                  *tgbotapi.BotAPI
	taskQueue            chan *tgbotapi.Message
	wg                   sync.WaitGroup
	downloader           ytDlp
}

func main() {
	var telegramBot TelegramBotConfig
	parser := flags.NewParser(&telegramBot, flags.IgnoreUnknown)
	_, err := parser.Parse()
	if err != nil {
		panic(fmt.Sprintf("Error while parsing configuration, %s", err))
	}
	if telegramBot.HttpPort != "" {
		go telegramBot.startHttpServer(telegramBot.HttpPort)
	}

	telegramBot.telegramUpdateWorker()
}

func (tgBot *TelegramBotConfig) startHttpServer(port string) {
	tgBot.httpMux = http.NewServeMux()

	server := &http.Server{
		Addr:    ":" + port,
		Handler: tgBot.httpMux,
	}
	defer server.Close()
	if tgBot.HealthEndpoint != "" {
		tgBot.startHealthHandler()
	}
	slog.Info(fmt.Sprintf("Starting http server on port %s", port))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error(fmt.Sprintf("Http server start failed: %v", err))
		panic(err)
	}

}

func (tgBot *TelegramBotConfig) startHealthHandler() {
	tgBot.httpMux.HandleFunc(tgBot.HealthEndpoint, tgBot.healthHandler)
}

func (tgBot *TelegramBotConfig) healthHandler(w http.ResponseWriter, r *http.Request) {
	status := "OK"
	code := http.StatusOK
	if !tgBot.health() {
		status = "Fail"
		code = http.StatusInternalServerError
	}
	response := status
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(response)
}

func (tgBot *TelegramBotConfig) health() bool {
	if _, err := tgBot.bot.GetMe(); err == nil {
		return true
	} else {
		return false
	}
}

func (tgBot *TelegramBotConfig) telegramUpdateWorker() {
	tgBot.taskQueue = make(chan *tgbotapi.Message, 100)
	tgBot.downloader = *newYtDlp()

	var err error
	tgBot.bot, err = tgbotapi.NewBotAPI(tgBot.BotToken)
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to create bot, aborting: %v", err))
		panic(err)
	}

	for id := 0; id < tgBot.BotWorkerCount; id++ {
		tgBot.wg.Add(1)
		go tgBot.worker(id)
	}

	tgBot.bot.Debug = tgBot.Debug
	slog.Info(fmt.Sprintf("Authorized on account %s", tgBot.bot.Self.UserName))

	// Настройка long polling
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := tgBot.bot.GetUpdatesChan(u)

	// Обработка входящих сообщений
	for update := range updates {
		slog.Debug(fmt.Sprintf("Get new message: %v", update.Message))
		if update.Message != nil {
			select {
			case tgBot.taskQueue <- update.Message:
				slog.Debug(fmt.Sprintf("Message %d added to queue. Queue len is %v", update.Message.MessageID, len(tgBot.taskQueue)))
			default:
				slog.Error(fmt.Sprintf("Queue is full, message %d dropped", update.Message.MessageID))
				tgBot.sendMessage(update.Message, "Queue is full, message dropped", tgbotapi.ModeHTML)
			}
		}
	}

	// Ожидание завершения всех воркеров
	close(tgBot.taskQueue)
	tgBot.wg.Wait()
}

func (tgBot *TelegramBotConfig) isUserAllowed(userID int64) bool {
	if len(tgBot.AllowedUserIDs) == 0 {
		slog.Error("‼️ allowedUserIDs variable is not set. Any user will be allowed ")
		return true
	}
	return slices.Contains(tgBot.AllowedUserIDs, userID)
}

func (tgBot *TelegramBotConfig) worker(id int) {
	defer tgBot.wg.Done()
	slog.Debug(fmt.Sprintf("Worker %d started", id))

	for message := range tgBot.taskQueue {
		slog.Debug(fmt.Sprintf("Worker %d processing message %d", id, message.MessageID))
		if !tgBot.isUserAllowed(message.Chat.ID) {
			slog.Error(fmt.Sprintf("ChatId is not allowed: %s", message))
			tgBot.sendMessage(message, "🛑 This bot is private", tgbotapi.ModeMarkdownV2)
			return
		}
		if message.IsCommand() {
			tgBot.handleCommand(message)

		} else {
			tgBot.handleExpression(message)
		}
	}

	slog.Info(fmt.Sprintf("Worker %d stopped", id))
}

func (tgBot *TelegramBotConfig) handleCommand(message *tgbotapi.Message) {
	var response string
	switch message.Command() {
	case "start":
		response = "Привет! Я бот-качатор! 🦾\n\n" +
			"⚠️ Внимание: видео обрабатываются очереди, пожалуйста, ожидайте результат.\n" +
			"📽️ Смотреть: сейчас я умею складывать видео в Jellyfin. В будущем научусь другому."
	case "help":
		response = "Просто отправь мне ссылку на видео 📺 (любой платформы) и я попробую его скачать.\n\n" +
			"✅ Можно отправить несколько ссылок через пробел.\n\n" +
			"❌ Ограничения:\n- Прервать скачивание нельзя. Все добавленое будет скачано или умрет в процессе по достижению ⏲️ таймаута"
	case "status":
		response = fmt.Sprintf("Статус системы:\n- Сообщений в очереди: %d\n- Активных воркеров: %d", len(tgBot.taskQueue), tgBot.BotWorkerCount)
	default:
		response = "Неизвестная команда"
	}
	tgBot.sendMessage(message, response, tgbotapi.ModeMarkdownV2)

}

func (tgBot *TelegramBotConfig) handleExpression(message *tgbotapi.Message) {
	expr := strings.TrimSpace(message.Text)
	if expr == "" {
		return
	}

	tgBot.downloader.inputStrings = []string{expr}
	tgBot.progressResponder(tgBot.downloader.runCommand(), message)

}

func (tgBot *TelegramBotConfig) progressResponder(ch <-chan CommandResult, message *tgbotapi.Message) {
	var str []string
	startTime := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	progressMessage, _ := tgBot.sendMessage(message, "⏳ Качаю, качаю, ожидайте\n<code>\n--+--+--+--+--+\n</code>", tgbotapi.ModeHTML)

	for {
		select {
		case value, ok := <-ch:
			if !ok {
				tgBot.updateMessage(&progressMessage, fmt.Sprintf("🏁 Результат:\n<code>\n%v\n</code>", strings.Join(str, "\n")), tgbotapi.ModeHTML)
				return
			}
			var itemPrefix = "✔️"
			if value.Error != nil {
				itemPrefix = fmt.Sprintf("❌ %s", value.Output)
			}
			str = append(str, fmt.Sprintf("%s %s\n", itemPrefix, value.FileName))
			tgBot.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n<code>\n%v\n</code>", strings.Join(str, "\n")), tgbotapi.ModeHTML)

		case <-ticker.C:
			processingTime := time.Since(startTime).Round(time.Second)
			if len(str) == 0 {
				if processingTime%3 == 0 {
					tgBot.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n<code>\n%v\n</code>", "+--+--+--+--+--"), tgbotapi.ModeHTML)
				} else if processingTime%3 == 1 {
					tgBot.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n<code>\n%v\n</code>", "-+--+--+--+--+-"), tgbotapi.ModeHTML)
				} else {
					tgBot.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n<code>\n%v\n</code>", "--+--+--+--+--+"), tgbotapi.ModeHTML)
				}
			}
		}
	}
}

func (tgBot *TelegramBotConfig) sendMessage(message *tgbotapi.Message, text string, parseMode string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(message.Chat.ID, tgBot.escapeMessageText(parseMode, text))
	msg.ParseMode = parseMode
	msg.DisableWebPagePreview = true
	msg.DisableNotification = true
	responseMessage, err := tgBot.bot.Send(msg)
	if err != nil {
		slog.Error(fmt.Sprintf("Error sending message: %v", err))
	}
	return responseMessage, err
}

func (tgBot *TelegramBotConfig) updateMessage(message *tgbotapi.Message, text string, parseMode string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewEditMessageText(message.Chat.ID, message.MessageID, tgBot.escapeMessageText(parseMode, text))
	msg.ParseMode = parseMode
	msg.DisableWebPagePreview = tgBot.EnableWebPagePreview
	updatedMsg, err := tgBot.bot.Send(msg)
	return updatedMsg, err
}

func (tgBot *TelegramBotConfig) escapeMessageText(parseMode string, text string) string {
	var msgText string
	if parseMode == tgbotapi.ModeMarkdownV2 {
		msgText = tgbotapi.EscapeText(tgbotapi.ModeMarkdownV2, text)
	} else {
		msgText = text
	}
	return msgText
}
