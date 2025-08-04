package main

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jessevdk/go-flags"
	"log"
	"slices"
	"strings"
	"sync"
	"time"
)

type TelegramBotConfig struct {
	BotToken         string  `long:"telegram-bot-token" env:"TELEGRAM_BOT_TOKEN" required:"true"`
	BotWorkerCount   int     `long:"worker-count" env:"WORKER_COUNT" default:"5"`
	AllowedUserIDs   []int64 `long:"allowed-chatids" env:"ALLOWED_IDS" env-delim:";"`
	Debug            bool    `long:"bot-debug"`
	bot              *tgbotapi.BotAPI
	taskQueue        chan *tgbotapi.Message
	wg               sync.WaitGroup
	downloader       ytDlp
	DownloaderConfig struct {
		BinaryPath         string `long:"yt-dlp-binary-path" env:"YT_DLP_BINARY" default:"./yt-dlp"`
		OutputPath         string `long:"yt-dlp-output-dir" env:"YT_DLP_OUTPUT_DIR" default:"./"`
		OutputType         string `long:"yt-dlp-output-format" env:"YT_DLP_OUTPUT_FORMAT" default:"mp4"`
		Proxy              string `long:"yt-dlp-proxy" env:"YT_DLP_PROXY" default:""`
		ProcessingTimeoutS int    `long:"yt-dlp-processing-timeout" env:"YT_DLP_PROCESSING_TIMEOUT" default:"300"`
	}
}

func main() {
	var telegramBot TelegramBotConfig
	parser := flags.NewParser(&telegramBot, flags.IgnoreUnknown)
	_, err := parser.Parse()
	if err != nil {
		panic(fmt.Sprintf("Error while parsing configuration, %s", err))
	}

	telegramBot.telegramUpdateWorker()
}

func (config *TelegramBotConfig) telegramUpdateWorker() {
	config.taskQueue = make(chan *tgbotapi.Message, 100)
	config.downloader = *newYtDlp()

	var err error
	config.bot, err = tgbotapi.NewBotAPI(config.BotToken)
	if err != nil {
		log.Panic(err)
	}

	for id := 0; id < config.BotWorkerCount; id++ {
		config.wg.Add(1)
		go config.worker(id)
	}

	config.bot.Debug = config.Debug
	log.Printf("Authorized on account %s", config.bot.Self.UserName)

	// Настройка long polling
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := config.bot.GetUpdatesChan(u)

	// Обработка входящих сообщений
	for update := range updates {
		log.Printf("%v", update.Message)
		if update.Message != nil {
			select {
			case config.taskQueue <- update.Message:
				log.Printf("Message %d added to queue", update.Message.MessageID)
			default:
				log.Printf("Queue is full, message %d dropped", update.Message.MessageID)
				config.sendMessage(update.Message, "Queue is full, message dropped")
			}
		}
	}

	// Ожидание завершения всех воркеров
	close(config.taskQueue)
	config.wg.Wait()
}

func (config *TelegramBotConfig) isUserAllowed(userID int64) bool {
	// Если нет ограничений - разрешаем всем
	if len(config.AllowedUserIDs) == 0 {
		log.Printf("‼️ allowedUserIDs variable is not set. Any user will be allowed ")
		return true
	}
	return slices.Contains(config.AllowedUserIDs, userID)
}

func (config *TelegramBotConfig) worker(id int) {
	defer config.wg.Done()
	log.Printf("Worker %d started", id)

	for message := range config.taskQueue {
		log.Printf("Worker %d processing message %d", id, message.MessageID)
		if !config.isUserAllowed(message.Chat.ID) {
			log.Printf(fmt.Sprintf("ChatId is not allowed: %s", message))
			config.sendMessage(message, "🛑 This bot is private")
			return
		}
		if message.IsCommand() {
			config.handleCommand(message)

		} else {
			config.handleExpression(message)
		}
	}

	log.Printf("Worker %d stopped", id)
}

func (config *TelegramBotConfig) handleCommand(message *tgbotapi.Message) {
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
		response = fmt.Sprintf("Статус системы:\n- Сообщений в очереди: %d\n- Активных воркеров: %d", len(config.taskQueue), config.BotWorkerCount)
	default:
		response = "Неизвестная команда"
	}
	config.sendMessage(message, response)

}

func (config *TelegramBotConfig) handleExpression(message *tgbotapi.Message) {
	//startTime := time.Now()
	expr := strings.TrimSpace(message.Text)
	if expr == "" {
		return
	}

	config.downloader.inputStrings = []string{expr}
	config.progressResponder(config.downloader.runCommand(), message)

}

func (config *TelegramBotConfig) progressResponder(ch <-chan CommandResult, message *tgbotapi.Message) {
	var str []string
	startTime := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	progressMessage, _ := config.sendMessage(message, "⏳ Качаю, качаю, ожидайте\n`\n--+--+--+--+--+\n`")

	for {
		select {
		case value, ok := <-ch:
			if !ok {
				config.updateMessage(&progressMessage, fmt.Sprintf("🏁 Результат:\n`\n%v\n`", strings.Join(str, "\n")))
				return
			}
			var itemPrefix = "✔️"
			if value.Error != nil {
				itemPrefix = "❌"
			}
			str = append(str, fmt.Sprintf("%s %s\n", itemPrefix, value.Output))
			config.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n`\n%v\n`", strings.Join(str, "\n")))

		case <-ticker.C:
			processingTime := time.Since(startTime).Round(time.Second)
			if len(str) == 0 {
				if processingTime%3 == 0 {
					config.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n`\n%v\n`", "+--+--+--+--+--"))
				} else if processingTime%3 == 1 {
					config.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n`\n%v\n`", "-+--+--+--+--+-"))
				} else {
					config.updateMessage(&progressMessage, fmt.Sprintf("⏳ Качаю, качаю, ожидайте\n`\n%v\n`", "--+--+--+--+--+"))
				}
			}
		}
	}
}

func (config *TelegramBotConfig) sendMessage(message *tgbotapi.Message, text string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(message.Chat.ID, text)
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.DisableWebPagePreview = true
	msg.DisableNotification = true
	responseMessage, err := config.bot.Send(msg)
	log.Printf("Message: %s\nBot: %s", text, config.bot)
	if err != nil {
		log.Printf("Error sending message: %v", err)
	}
	return responseMessage, err
}

func (config *TelegramBotConfig) updateMessage(message *tgbotapi.Message, text string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewEditMessageText(message.Chat.ID, message.MessageID, text)
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.DisableWebPagePreview = true
	updatedMsg, err := config.bot.Send(msg)
	return updatedMsg, err
}
