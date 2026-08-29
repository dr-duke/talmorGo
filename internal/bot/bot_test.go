package bot

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/queue"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
	"github.com/dr-duke/talmorGo/internal/storage"
)

// fakeAPI записывает всё, что бот отправляет в Telegram.
type fakeAPI struct {
	mu       sync.Mutex
	sent     []tgbotapi.Chattable
	nextMsg  int
	requests []tgbotapi.Chattable
}

func (f *fakeAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, c)
	f.nextMsg++
	return tgbotapi.Message{MessageID: f.nextMsg}, nil
}

func (f *fakeAPI) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, c)
	return &tgbotapi.APIResponse{Ok: true}, nil
}

// texts возвращает тексты отправленных сообщений.
func (f *fakeAPI) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.sent {
		switch m := c.(type) {
		case tgbotapi.MessageConfig:
			out = append(out, m.Text)
		case tgbotapi.EditMessageTextConfig:
			out = append(out, m.Text)
		}
	}
	return out
}

func (f *fakeAPI) edits() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.sent {
		if m, ok := c.(tgbotapi.EditMessageTextConfig); ok {
			out = append(out, m.Text)
		}
	}
	return out
}

func (f *fakeAPI) containing(substr string) bool {
	for _, t := range f.texts() {
		if strings.Contains(t, substr) {
			return true
		}
	}
	return false
}

type botEnv struct {
	bot   *Bot
	api   *fakeAPI
	jobs  repo.JobRepo
	queue *queue.Service
}

// waitFor ждёт выполнения условия — разворачивание ссылки идёт в фоне.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("условие не выполнилось за отведённое время")
}

func newBotEnv(t *testing.T) *botEnv {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Бинаря yt-dlp нет: проверка плейлиста не удаётся и ссылка считается
	// одиночным видео — этого достаточно для сценариев бота.
	cfg := &config.Config{
		YtDlpBinary:        filepath.Join(dir, "no-such-yt-dlp"),
		YtDlpOutputDir:     dir,
		YtDlpTimeout:       5,
		BaseURL:            "https://media.example.com",
		TelegramAllowedIDs: []int64{100},
	}

	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	q := &queue.Service{
		Jobs: jobs, Items: items, Storage: storage.New(dir),
		Expander: playlist.New(jobs, repo.NewTagRepo(database)),
		Pool:     noopPool{},
		Settings: settings.New(cfg, repo.NewSettingsRepo(database)),
	}

	api := &fakeAPI{}
	b := &Bot{
		cfg: cfg, api: api, jobs: jobs, tokens: repo.NewTokenRepo(database),
		queue: q, pendingMsgs: make(map[string]int64),
	}
	q.SetObserver(b)

	return &botEnv{bot: b, api: api, jobs: jobs, queue: q}
}

type noopPool struct{}

func (noopPool) Enqueue()              {}
func (noopPool) CancelJob(string) bool { return false }

func msg(chatID int64, text string) *tgbotapi.Message {
	return &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: chatID},
		Text: text,
	}
}

// Чужой чат бот не обслуживает.
func TestBot_RejectsForeignChat(t *testing.T) {
	e := newBotEnv(t)
	e.bot.handleMessage(context.Background(), msg(999, "https://example.com/v"))

	if !e.api.containing("private") {
		t.Errorf("ожидался отказ, отправлено: %v", e.api.texts())
	}
	jobs, _ := e.jobs.List(context.Background(), repo.JobFilter{})
	if len(jobs) != 0 {
		t.Errorf("создано %d заданий для чужого чата", len(jobs))
	}
}

// Ссылка ставится в очередь, пользователю уходит сообщение с кнопкой отмены.
func TestBot_QueuesURL(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()

	e.bot.handleMessage(ctx, msg(100, "https://example.com/video"))

	jobs, _ := e.jobs.List(ctx, repo.JobFilter{})
	if len(jobs) != 1 {
		t.Fatalf("заданий = %d, ожидалось 1", len(jobs))
	}
	if jobs[0].Source != "telegram" || jobs[0].ChatID != 100 {
		t.Errorf("задание = %+v, ожидались source=telegram и chat_id=100", jobs[0])
	}
	if !e.api.containing("В очереди") {
		t.Errorf("сообщение об очереди не отправлено: %v", e.api.texts())
	}

	// id сообщения сохраняется, чтобы потом его отредактировать.
	waitFor(t, func() bool {
		j, err := e.jobs.GetByID(ctx, jobs[0].ID)
		return err == nil && j.TgMessageID != 0
	})
}

// Несколько ссылок в одном сообщении, часть — мусор.
func TestBot_HandlesMixedInput(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()

	e.bot.handleMessage(ctx, msg(100, "https://example.com/a не-ссылка https://example.com/b"))

	jobs, _ := e.jobs.List(ctx, repo.JobFilter{})
	if len(jobs) != 2 {
		t.Errorf("заданий = %d, ожидалось 2", len(jobs))
	}
	if !e.api.containing("Пропущено") {
		t.Errorf("не сообщено о пропущенной строке: %v", e.api.texts())
	}
}

// Совсем без ссылок — понятный ответ и ни одного задания.
func TestBot_NoValidLinks(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()

	e.bot.handleMessage(ctx, msg(100, "просто текст"))

	jobs, _ := e.jobs.List(ctx, repo.JobFilter{})
	if len(jobs) != 0 {
		t.Errorf("создано %d заданий на текст без ссылок", len(jobs))
	}
	if !e.api.containing("Не найдено корректных ссылок") {
		t.Errorf("ответ = %v", e.api.texts())
	}
}

// Если ссылка оказалась плейлистом, сообщение «в очереди» заменяется сводкой.
func TestBot_OnExpandedReplacesMessageWithSummary(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()

	e.bot.rememberPending("job-1", 55)
	e.bot.OnExpanded(ctx, queue.ExpandEvent{
		PlaceholderID: "job-1",
		ChatID:        100,
		IsPlaylist:    true,
		PlaylistTitle: "Лекции по Go",
		Created:       7,
	})

	edits := e.api.edits()
	if len(edits) != 1 {
		t.Fatalf("правок сообщения = %d, ожидалась 1", len(edits))
	}
	if !strings.Contains(edits[0], "Лекции по Go") || !strings.Contains(edits[0], "7") {
		t.Errorf("сводка = %q, ожидались название и число видео", edits[0])
	}
}

// Для одиночного видео сообщение не трогаем — его ведёт пул загрузок.
func TestBot_OnExpandedIgnoresSingleVideo(t *testing.T) {
	e := newBotEnv(t)

	e.bot.rememberPending("job-1", 55)
	e.bot.OnExpanded(context.Background(), queue.ExpandEvent{
		PlaceholderID: "job-1",
		ChatID:        100,
		IsPlaylist:    false,
	})

	if edits := e.api.edits(); len(edits) != 0 {
		t.Errorf("сообщение отредактировано зря: %v", edits)
	}
}

// Кнопка «Отменить» действительно отменяет задание.
func TestBot_CallbackCancelsJob(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/v", Status: model.JobPending, Source: "telegram", ChatID: 100}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}

	e.bot.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 100},
		Data:    "stop:" + job.ID,
		Message: &tgbotapi.Message{MessageID: 5, Chat: &tgbotapi.Chat{ID: 100}},
	})

	got, _ := e.jobs.GetByID(ctx, job.ID)
	if got.Status != model.JobCancelled {
		t.Errorf("статус = %s, ожидался cancelled", got.Status)
	}
}

// Кнопка «Повторить» возвращает упавшее задание в очередь.
func TestBot_CallbackRetriesFailedJob(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/v", Status: model.JobFailed, Source: "telegram", ChatID: 100}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}

	e.bot.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:      "cb2",
		From:    &tgbotapi.User{ID: 100},
		Data:    "retry:" + job.ID,
		Message: &tgbotapi.Message{MessageID: 6, Chat: &tgbotapi.Chat{ID: 100}},
	})

	got, _ := e.jobs.GetByID(ctx, job.ID)
	if got.Status != model.JobPending {
		t.Errorf("статус = %s, ожидался pending", got.Status)
	}
}

// Команда /status показывает счётчики по статусам.
func TestBot_StatusCommand(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()

	for _, st := range []model.JobStatus{model.JobPending, model.JobPending, model.JobDone} {
		if err := e.jobs.Create(ctx, &model.Job{URL: "https://e.com/x", Status: st, Source: "web"}); err != nil {
			t.Fatal(err)
		}
	}

	e.bot.handleMessage(ctx, &tgbotapi.Message{
		Chat:     &tgbotapi.Chat{ID: 100},
		Text:     "/status",
		Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 7}},
	})

	texts := e.api.texts()
	if len(texts) == 0 || !strings.Contains(texts[0], "Статус очереди") {
		t.Fatalf("ответ = %v", texts)
	}
	if !strings.Contains(texts[0], "Ожидание: 2") {
		t.Errorf("счётчик ожидающих неверен: %q", texts[0])
	}
}

// Опасные символы в пользовательском тексте экранируются: сообщения уходят с ParseMode=HTML.
func TestBot_EscapesHTMLInSearchQuery(t *testing.T) {
	e := newBotEnv(t)

	e.bot.handleSearch(context.Background(), 100, "<script>alert(1)</script>")

	texts := e.api.texts()
	if len(texts) == 0 {
		t.Fatal("ответ не отправлен")
	}
	if strings.Contains(texts[0], "<script>") {
		t.Errorf("HTML не экранирован: %q", texts[0])
	}
}
