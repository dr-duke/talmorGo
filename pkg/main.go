package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

//sourceList := []string{
//"https://youtu.be/fregObNcHC8?si=i6UOeeOX1-nfsnS1",
//"https://youtu.be/YlUKcNNmywk?si=C5sBoPvGMcIR0I6Y https://youtu.be/hQ_Z-10dXSE?si=iPkeHajH-A064gCq",
//}

var (
	bot         *tgbotapi.BotAPI
	taskQueue   chan *tgbotapi.Message // Очередь задач
	workerCount = 2                    // Количество воркеров
	wg          sync.WaitGroup
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable not set")
	}

	// Инициализация очереди задач
	taskQueue = make(chan *tgbotapi.Message, 100)

	// Запуск воркеров
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go worker(i)
	}

	var err error
	bot, err = tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Настройка long polling
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	// Обработка входящих сообщений
	for update := range updates {
		if update.Message != nil {
			select {
			case taskQueue <- update.Message:
				log.Printf("Message %d added to queue", update.Message.MessageID)
			default:
				log.Printf("Queue is full, message %d dropped", update.Message.MessageID)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Сервер перегружен, попробуйте позже")
				bot.Send(msg)
			}
		}
	}

	// Ожидание завершения всех воркеров
	close(taskQueue)
	wg.Wait()
}

func worker(id int) {
	defer wg.Done()
	log.Printf("Worker %d started", id)

	for message := range taskQueue {
		log.Printf("Worker %d processing message %d", id, message.MessageID)
		// todo: useris filter
		if message.IsCommand() {
			handleCommand(message)
		} else {
			handleExpression(message)
		}
	}

	log.Printf("Worker %d stopped", id)
}

func handleCommand(message *tgbotapi.Message) {
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	switch message.Command() {
	case "start":
		msg.Text = "Привет! Я бот-качатор! 🦾\n\n" +
			"⚠️ Внимание: видео обрабатываются очереди, пожалуйста, ожидайте результат.\n" +
			"📽️ Смотреть: сейчас я умею складывать видео в Jellyfin. В будущем научусь другому."
	case "help":
		msg.Text = "Просто отправь мне ссылку на видео 📺 (любой платформы) и я попробую его скачать.\n\n" +
			"✅ Можно отправить несколько ссылок через пробел.\n\n" +
			"❌ Ограничения:\n- Прервать скачивание нельзя. Все добавленое будет скачано или умрет в процессе по достижению ⏲️ таймаута"
	case "status":
		msg.Text = fmt.Sprintf("Статус системы:\n- Сообщений в очереди: %d\n- Активных воркеров: %d", len(taskQueue), workerCount)
	default:
		msg.Text = "Неизвестная команда"
	}

	if _, err := bot.Send(msg); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

func handleExpression(message *tgbotapi.Message) {
	startTime := time.Now()
	expr := strings.TrimSpace(message.Text)
	if expr == "" {
		return
	}

	// Отправка уведомления о начале обработки
	processingMsg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("⏳ Качаю, качаю, ожидайте: %s...", expr))
	processingMsg.DisableWebPagePreview = true
	sentMsg, _ := bot.Send(processingMsg)

	var ytDlp = *newYtDlp()
	ytDlp.setOutputPath(os.Getenv("YT_DLP_OUTPUT_DIR"))
	ytDlp.setOutputType(os.Getenv("YT_DLP_OUTPUT_FORMAT"))
	ytDlp.setBinaryPath(os.Getenv("YT_DLP_BINARY"))
	if os.Getenv("YT_DLP_PROXY") != "" {
		ytDlp.setProxy(os.Getenv("YT_DLP_PROXY"))
	}
	ytDlp.inputStrings = []string{expr}
	res := ytDlp.runCommand()

	// Удаление сообщения о процессе обработки
	deleteMsg := tgbotapi.NewDeleteMessage(message.Chat.ID, sentMsg.MessageID)
	bot.Send(deleteMsg)

	response := tgbotapi.NewMessage(message.Chat.ID, "")

	var answerText []string
	for _, result := range res {
		if result.Error != nil {
			answerText = append(answerText, fmt.Sprintf("❌ %s\n", result.Output))
		} else if result.Output != "" {
			answerText = append(answerText, fmt.Sprintf("✔️ %s\n", result.Output))
		}
	}
	slog.Info(fmt.Sprintf("%s", res))
	processingTime := time.Since(startTime).Round(time.Millisecond)
	response.Text = fmt.Sprintf("⏱ Время обработки: %v\nРезультат:\n\n", processingTime)
	for idx, line := range answerText {
		response.Text = response.Text + fmt.Sprintf("%v. %s\n", idx+1, line)
	}

	response.ParseMode = "Markdown"
	if _, err := bot.Send(response); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}
