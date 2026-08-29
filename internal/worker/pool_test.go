package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
)

// fakeDownloader изображает yt-dlp: кладёт файлы в staging и отдаёт события.
type fakeDownloader struct {
	files   []string
	err     error
	logLine string

	// block удерживает «загрузку», пока канал не закроют или не отменят контекст.
	block   chan struct{}
	started chan struct{}

	mu    sync.Mutex
	calls int
}

func (f *fakeDownloader) Run(ctx context.Context, _ string, opts downloader.Options) <-chan downloader.Event {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	ch := make(chan downloader.Event, 8)
	go func() {
		defer close(ch)

		if f.started != nil {
			close(f.started)
			f.started = nil
		}
		if f.block != nil {
			select {
			case <-f.block:
			case <-ctx.Done():
				return
			}
		}

		for _, name := range f.files {
			path := filepath.Join(opts.OutputDir, name)
			if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
				ch <- downloader.Event{Err: err}
				return
			}
			ch <- downloader.Event{FileName: name, Path: path}
		}
		if f.logLine != "" {
			ch <- downloader.Event{Log: f.logLine}
		}
		if f.err != nil {
			ch <- downloader.Event{Err: f.err}
		}
	}()
	return ch
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type recordingNotifier struct {
	mu   sync.Mutex
	sent []Notification
}

func (n *recordingNotifier) Notify(_ context.Context, notif Notification) {
	n.mu.Lock()
	n.sent = append(n.sent, notif)
	n.mu.Unlock()
}

func (n *recordingNotifier) kinds() []NotifKind {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]NotifKind, 0, len(n.sent))
	for _, s := range n.sent {
		out = append(out, s.Kind)
	}
	return out
}

type poolEnv struct {
	pool    *Pool
	jobs    repo.JobRepo
	items   repo.ItemRepo
	dataDir string
}

func newPoolEnv(t *testing.T) *poolEnv {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := &config.Config{
		YtDlpOutputDir:   dir,
		YtDlpStagingDir:  filepath.Join(dir, ".staging"),
		YtDlpBinary:      "yt-dlp",
		YtDlpTimeout:     60,
		WorkerCount:      1,
		RetryBackoffBase: 30,
		RetryMaxDuration: 86400,
	}

	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	p := NewPool(cfg, jobs, items, repo.NewTokenRepo(database), nil)
	p.SetSettings(settings.New(cfg, repo.NewSettingsRepo(database)))

	return &poolEnv{pool: p, jobs: jobs, items: items, dataDir: dir}
}

func (e *poolEnv) newJob(t *testing.T, source string, chatID int64) *model.Job {
	t.Helper()
	job := &model.Job{
		URL:    "https://example.com/video",
		Status: model.JobRunning,
		Source: source,
		ChatID: chatID,
	}
	if err := e.jobs.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	return job
}

// Успешная загрузка: файл переезжает из staging в медиатеку, появляется запись,
// заголовок задания берётся из имени первого файла.
func TestPool_SavesDownloadedFile(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	e.pool.SetDownloader(&fakeDownloader{files: []string{"Видео.mp4"}})
	e.pool.process(ctx, job)

	got, err := e.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.JobDone {
		t.Errorf("статус = %s, ожидался done", got.Status)
	}
	if got.Title != "Видео.mp4" {
		t.Errorf("title = %q, ожидалось имя файла", got.Title)
	}

	final := filepath.Join(e.dataDir, "Видео.mp4")
	if _, err := os.Stat(final); err != nil {
		t.Errorf("файл не перенесён в медиатеку: %v", err)
	}

	list, _ := e.items.ListByJobID(ctx, job.ID)
	if len(list) != 1 {
		t.Fatalf("записей = %d, ожидалась 1", len(list))
	}
	if list[0].Path != final {
		t.Errorf("path = %q, ожидался %q", list[0].Path, final)
	}
	if list[0].Size == 0 {
		t.Error("размер файла не заполнен")
	}

	// Staging после задания убирается.
	if _, err := os.Stat(filepath.Join(e.dataDir, ".staging", job.ID)); !os.IsNotExist(err) {
		t.Error("каталог staging остался после задания")
	}
}

// Несколько файлов одной ссылки становятся отдельными записями.
func TestPool_SavesEveryFileOfPlaylist(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	e.pool.SetDownloader(&fakeDownloader{files: []string{"a.mp4", "b.mp4", "c.mp4"}})
	e.pool.process(ctx, job)

	list, _ := e.items.ListByJobID(ctx, job.ID)
	if len(list) != 3 {
		t.Errorf("записей = %d, ожидалось 3", len(list))
	}
}

// Ошибка загрузки переводит задание в retrying и назначает время следующей попытки.
func TestPool_SchedulesRetryWithBackoff(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	e.pool.SetClock(fixedClock{now})
	e.pool.SetDownloader(&fakeDownloader{err: errors.New("network unreachable")})

	e.pool.process(ctx, job)

	got, _ := e.jobs.GetByID(ctx, job.ID)
	if got.Status != model.JobRetrying {
		t.Fatalf("статус = %s, ожидался retrying", got.Status)
	}
	if got.RetryCount != 1 {
		t.Errorf("попытка = %d, ожидалась 1", got.RetryCount)
	}
	if got.Error == "" {
		t.Error("текст ошибки не сохранён")
	}
	if got.NextRetryAt == nil {
		t.Fatal("время следующей попытки не назначено")
	}
	// Первая попытка: base << 0 = 30 секунд.
	want := now.Add(30 * time.Second)
	if !got.NextRetryAt.Equal(want) {
		t.Errorf("следующая попытка = %v, ожидалось %v", got.NextRetryAt.UTC(), want)
	}
}

// Интервал растёт экспоненциально с номером попытки.
func TestPool_BackoffGrowsWithAttempts(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	firstFailed := now.Add(-time.Hour)
	job.RetryCount = 3
	job.FirstFailedAt = &firstFailed

	e.pool.SetClock(fixedClock{now})
	e.pool.SetDownloader(&fakeDownloader{err: errors.New("boom")})
	e.pool.process(ctx, job)

	got, _ := e.jobs.GetByID(ctx, job.ID)
	// Четвёртая попытка: base << 3 = 240 секунд.
	want := now.Add(240 * time.Second)
	if got.NextRetryAt == nil || !got.NextRetryAt.Equal(want) {
		t.Errorf("следующая попытка = %v, ожидалось %v", got.NextRetryAt, want)
	}
}

// За пределами окна повторов задание окончательно падает.
func TestPool_FailsWhenRetryWindowExhausted(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	e.pool.cfg.RetryMaxDuration = 0 // окно исчерпано сразу
	e.pool.SetDownloader(&fakeDownloader{err: errors.New("gone")})
	e.pool.process(ctx, job)

	got, _ := e.jobs.GetByID(ctx, job.ID)
	if got.Status != model.JobFailed {
		t.Errorf("статус = %s, ожидался failed", got.Status)
	}
	if got.NextRetryAt != nil {
		t.Error("у окончательно упавшего задания не должно быть времени повтора")
	}
}

// Частичный успех плейлиста ошибкой не считается.
func TestPool_PartialSuccessIsNotFailure(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	e.pool.SetDownloader(&fakeDownloader{
		files: []string{"ok.mp4"},
		err:   errors.New("одно из видео недоступно"),
	})
	e.pool.process(ctx, job)

	got, _ := e.jobs.GetByID(ctx, job.ID)
	if got.Status != model.JobDone {
		t.Errorf("статус = %s, ожидался done", got.Status)
	}
}

// Лог последней попытки сохраняется и доступен из интерфейса.
func TestPool_SavesLog(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	e.pool.SetDownloader(&fakeDownloader{files: []string{"v.mp4"}, logLine: "ERROR: что-то пошло не так"})
	e.pool.process(ctx, job)

	log, err := e.jobs.GetLog(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if log == "" {
		t.Error("лог не сохранён")
	}
}

// Отмена во время загрузки останавливает процесс и помечает задание отменённым.
func TestPool_CancelStopsRunningJob(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "web", 0)

	started := make(chan struct{})
	fake := &fakeDownloader{
		files:   []string{"должен-остаться-неснятым.mp4"},
		block:   make(chan struct{}),
		started: started,
	}
	e.pool.SetDownloader(fake)

	go func() {
		<-started
		e.pool.CancelJob(job.ID)
	}()
	e.pool.process(ctx, job)

	got, _ := e.jobs.GetByID(ctx, job.ID)
	if got.Status != model.JobCancelled {
		t.Errorf("статус = %s, ожидался cancelled", got.Status)
	}
	list, _ := e.items.ListByJobID(ctx, job.ID)
	if len(list) != 0 {
		t.Errorf("записей = %d, у отменённого задания файлов быть не должно", len(list))
	}
}

// Задание из Telegram сопровождается уведомлениями на каждом шаге.
func TestPool_NotifiesTelegram(t *testing.T) {
	e := newPoolEnv(t)
	ctx := context.Background()
	job := e.newJob(t, "telegram", 4242)

	notifier := &recordingNotifier{}
	e.pool.SetNotifier(notifier)
	e.pool.SetDownloader(&fakeDownloader{files: []string{"v.mp4"}})
	e.pool.process(ctx, job)

	kinds := notifier.kinds()
	want := []NotifKind{NotifJobStarted, NotifFileDone, NotifJobDone}
	if len(kinds) != len(want) {
		t.Fatalf("уведомления = %v, ожидались %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("уведомление %d = %v, ожидалось %v", i, kinds[i], want[i])
		}
	}
}

// Веб-задание уведомлений в Telegram не порождает.
func TestPool_WebJobIsNotNotified(t *testing.T) {
	e := newPoolEnv(t)
	job := e.newJob(t, "web", 0)

	notifier := &recordingNotifier{}
	e.pool.SetNotifier(notifier)
	e.pool.SetDownloader(&fakeDownloader{files: []string{"v.mp4"}})
	e.pool.process(context.Background(), job)

	if got := notifier.kinds(); len(got) != 0 {
		t.Errorf("для web-задания отправлены уведомления: %v", got)
	}
}

// При ошибке пользователь получает сообщение о повторе.
func TestPool_NotifiesRetry(t *testing.T) {
	e := newPoolEnv(t)
	job := e.newJob(t, "telegram", 7)

	notifier := &recordingNotifier{}
	e.pool.SetNotifier(notifier)
	e.pool.SetDownloader(&fakeDownloader{err: errors.New("timeout")})
	e.pool.process(context.Background(), job)

	kinds := notifier.kinds()
	if len(kinds) == 0 || kinds[len(kinds)-1] != NotifJobRetrying {
		t.Errorf("уведомления = %v, ожидалось завершение NotifJobRetrying", kinds)
	}
}
