package queue_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/queue"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
	"github.com/dr-duke/talmorGo/internal/storage"
)

// fakePool записывает обращения к пулу загрузок.
type fakePool struct {
	mu        sync.Mutex
	enqueued  int
	cancelled []string
	// running — задания, которые пул считает выполняющимися.
	running map[string]bool
}

func newFakePool() *fakePool { return &fakePool{running: map[string]bool{}} }

func (p *fakePool) Enqueue() {
	p.mu.Lock()
	p.enqueued++
	p.mu.Unlock()
}

func (p *fakePool) CancelJob(jobID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelled = append(p.cancelled, jobID)
	return p.running[jobID]
}

func (p *fakePool) cancelledIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.cancelled...)
}

// recordingObserver собирает события разворачивания.
type recordingObserver struct {
	mu     sync.Mutex
	events []queue.ExpandEvent
	done   chan struct{}
}

func newObserver() *recordingObserver {
	return &recordingObserver{done: make(chan struct{}, 8)}
}

func (o *recordingObserver) OnExpanded(_ context.Context, ev queue.ExpandEvent) {
	o.mu.Lock()
	o.events = append(o.events, ev)
	o.mu.Unlock()
	o.done <- struct{}{}
}

type env struct {
	svc      *queue.Service
	jobs     repo.JobRepo
	items    repo.ItemRepo
	pool     *fakePool
	observer *recordingObserver
	dataDir  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	// Бинаря yt-dlp в тестах нет: проверка плейлиста не удаётся, и ссылка
	// трактуется как одиночное видео — ровно тот путь, который здесь нужен.
	return newEnvBin(t, "")
}

// newEnvBin позволяет подставить бинарь-заглушку, чтобы пройти плейлистный путь.
func newEnvBin(t *testing.T, binary string) *env {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if binary == "" {
		binary = filepath.Join(dir, "no-such-yt-dlp")
	}
	cfg := &config.Config{
		YtDlpBinary:    binary,
		YtDlpOutputDir: dir,
		YtDlpTimeout:   5,
	}

	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	pool := newFakePool()
	obs := newObserver()

	svc := &queue.Service{
		Jobs: jobs, Items: items,
		Storage:  storage.New(dir),
		Expander: playlist.New(jobs, repo.NewTagRepo(database)),
		Pool:     pool,
		Settings: settings.New(cfg, repo.NewSettingsRepo(database)),
	}
	svc.SetObserver(obs)

	return &env{svc: svc, jobs: jobs, items: items, pool: pool, observer: obs, dataDir: dir}
}

// waitExpanded ждёт события разворачивания.
func (e *env) waitExpanded(t *testing.T) queue.ExpandEvent {
	t.Helper()
	select {
	case <-e.observer.done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for expansion")
	}
	e.observer.mu.Lock()
	defer e.observer.mu.Unlock()
	return e.observer.events[len(e.observer.events)-1]
}

func TestAdd_RejectsInvalidURL(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Add(context.Background(), "not a url", "web", 0); err == nil {
		t.Error("expected an error for a non-URL string")
	}
}

// Ссылка ставится в очередь сразу, разворачивание идёт в фоне.
func TestAdd_CreatesCheckingThenPending(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	job, err := e.svc.Add(ctx, "https://example.com/video", "web", 0)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if job.Status != model.JobChecking {
		t.Errorf("status right after add = %s, want checking", job.Status)
	}

	e.waitExpanded(t)

	got, err := e.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.JobPending {
		t.Errorf("status after expansion = %s, want pending", got.Status)
	}
}

// Наблюдатель нужен Telegram-боту, чтобы заменить сообщение сводкой по плейлисту.
func TestResolve_NotifiesObserver(t *testing.T) {
	e := newEnv(t)
	job, err := e.svc.Add(context.Background(), "https://example.com/single", "telegram", 4242)
	if err != nil {
		t.Fatal(err)
	}

	ev := e.waitExpanded(t)
	if ev.PlaceholderID != job.ID {
		t.Errorf("placeholder id = %s, want %s", ev.PlaceholderID, job.ID)
	}
	if ev.ChatID != 4242 {
		t.Errorf("chat id = %d, want 4242", ev.ChatID)
	}
	if ev.IsPlaylist {
		t.Error("single video must not be reported as a playlist")
	}
}

// Массовая отмена обязана останавливать и текущие загрузки: раньше процесс
// yt-dlp продолжал работу и перезаписывал статус на done.
func TestCancelAll_StopsRunningDownloads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	running := &model.Job{URL: "https://example.com/run", Status: model.JobRunning, Source: "web"}
	if err := e.jobs.Create(ctx, running); err != nil {
		t.Fatal(err)
	}
	pending := &model.Job{URL: "https://example.com/wait", Status: model.JobPending, Source: "web"}
	if err := e.jobs.Create(ctx, pending); err != nil {
		t.Fatal(err)
	}
	e.pool.running[running.ID] = true

	n, err := e.svc.CancelAll(ctx)
	if err != nil {
		t.Fatalf("cancel all: %v", err)
	}
	if n != 2 {
		t.Errorf("cancelled %d jobs, want 2", n)
	}

	var sawRunning bool
	for _, id := range e.pool.cancelledIDs() {
		if id == running.ID {
			sawRunning = true
		}
	}
	if !sawRunning {
		t.Error("running download was not stopped in the pool")
	}

	got, _ := e.jobs.GetByID(ctx, running.ID)
	if got.Status != model.JobCancelled {
		t.Errorf("running job status = %s, want cancelled", got.Status)
	}
}

// Redownload убирает старые файлы и возвращает задание к проверке ссылки.
func TestRedownload_ClearsFilesAndRequeues(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/again", Status: model.JobDone, Source: "web"}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dataDir, "old.mp4")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.items.Create(ctx, &model.Item{
		JobID: job.ID, Kind: "video", Path: path, Name: "old.mp4",
	}); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.Redownload(ctx, job.ID); err != nil {
		t.Fatalf("redownload: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("old file must be deleted")
	}
	left, _ := e.items.ListByJobID(ctx, job.ID)
	if len(left) != 0 {
		t.Errorf("items after redownload = %d, want 0", len(left))
	}

	e.waitExpanded(t)
	got, _ := e.jobs.GetByID(ctx, job.ID)
	if got.Status != model.JobPending {
		t.Errorf("status after redownload = %s, want pending", got.Status)
	}
}

// Задания, оборванные на проверке плейлиста, должны разворачиваться заново.
func TestRecoverChecking(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	stale := &model.Job{URL: "https://example.com/stale", Status: model.JobChecking, Source: "web"}
	if err := e.jobs.Create(ctx, stale); err != nil {
		t.Fatal(err)
	}

	e.svc.RecoverChecking(ctx)
	e.waitExpanded(t)

	got, _ := e.jobs.GetByID(ctx, stale.ID)
	if got.Status != model.JobPending {
		t.Errorf("recovered job status = %s, want pending", got.Status)
	}
}

// stubPlaylistBinary кладёт заглушку yt-dlp, отдающую плейлист из двух видео
// в формате --flat-playlist (три строки на запись). Пауза перед выводом даёт
// тесту успеть нажать «Отменить», пока идёт проверка.
func stubPlaylistBinary(t *testing.T, delay string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "yt-dlp-stub.sh")
	script := "#!/bin/sh\nsleep " + delay + "\n" +
		"echo https://example.com/a\necho 'Видео A'\necho 'Сборник'\n" +
		"echo https://example.com/b\necho 'Видео B'\necho 'Сборник'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCancelDuringChecking_NoJobsCreated: отмена во время проверки должна
// останавливать разворачивание. Раньше отмена лишь меняла статус заготовки, а
// фоновая горутина всё равно доходила до конца и заводила задание на каждое
// видео — пользователь отменял ссылку на плейлист и через пару секунд получал
// полную очередь.
func TestCancelDuringChecking_NoJobsCreated(t *testing.T) {
	e := newEnvBin(t, stubPlaylistBinary(t, "1"))
	ctx := context.Background()

	job, err := e.svc.Add(ctx, "https://example.com/playlist", "web", 0)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := e.svc.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	e.waitExpanded(t)

	all, err := e.jobs.List(ctx, repo.JobFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, j := range all {
		if j.ID != job.ID {
			t.Errorf("после отмены создано задание %s (%s)", j.ID, j.URL)
		}
	}

	got, err := e.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.JobCancelled {
		t.Errorf("статус заготовки = %s, ожидался cancelled", got.Status)
	}
}

// TestPlaylistExpandsWhenNotCancelled — контрольный: без отмены тот же путь
// обязан развернуть плейлист, иначе предыдущий тест проходил бы впустую.
func TestPlaylistExpandsWhenNotCancelled(t *testing.T) {
	e := newEnvBin(t, stubPlaylistBinary(t, "0"))
	ctx := context.Background()

	if _, err := e.svc.Add(ctx, "https://example.com/playlist", "web", 0); err != nil {
		t.Fatalf("add: %v", err)
	}
	ev := e.waitExpanded(t)
	if !ev.IsPlaylist {
		t.Fatal("плейлист не распознан — заглушка не сработала")
	}
	if ev.Created != 2 {
		t.Errorf("создано заданий: %d, ожидалось 2", ev.Created)
	}
}
