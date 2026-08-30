package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/library"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
	"github.com/dr-duke/talmorGo/internal/storage"
)

// fakeRunner считает обращения к исполнителю фоновых операций.
type fakeRunner struct {
	enqueued  int
	cancelled []string
	canCancel bool
}

func (f *fakeRunner) Enqueue() { f.enqueued++ }
func (f *fakeRunner) Cancel(id string) bool {
	f.cancelled = append(f.cancelled, id)
	return f.canCancel
}

type env struct {
	svc     *library.Service
	jobs    repo.JobRepo
	items   repo.ItemRepo
	ops     repo.OperationRepo
	runner  *fakeRunner
	dataDir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := &config.Config{
		YtDlpOutputDir: dir,
		BaseURL:        "https://media.example.com",
		BasePath:       "/talmor",
		LibPageSize:    200,
	}

	e := &env{
		jobs:    repo.NewJobRepo(database),
		items:   repo.NewItemRepo(database),
		ops:     repo.NewOperationRepo(database),
		runner:  &fakeRunner{},
		dataDir: dir,
	}
	e.svc = &library.Service{
		Jobs: e.jobs, Items: e.items,
		Tags:        repo.NewTagRepo(database),
		Tokens:      repo.NewTokenRepo(database),
		Collections: repo.NewCollectionRepo(database),
		Ops:         e.ops,
		Storage:     storage.New(dir),
		Settings:    settings.New(cfg, repo.NewSettingsRepo(database)),
		Cfg:         cfg,
		Runner:      e.runner,
	}
	return e
}

// addFile создаёт задание с файлом на диске.
func (e *env) addFile(t *testing.T, name string) (*model.Job, *model.Item) {
	t.Helper()
	ctx := context.Background()
	job := &model.Job{URL: "https://example.com/" + name, Status: model.JobDone, Source: "web"}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dataDir, name)
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	item := &model.Item{JobID: job.ID, Kind: "video", Path: path, Name: name, Size: 4}
	if err := e.items.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	return job, item
}

// Удаление стирает файл с диска, но сохраняет запись: ссылка и имя должны остаться.
func TestDeleteItem_RemovesFileKeepsRecord(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, item := e.addFile(t, "clip.mp4")

	if err := e.svc.DeleteItem(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Error("file must be removed from disk")
	}

	deleted, err := e.svc.ListDeleted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0].OriginalURL == "" {
		t.Errorf("deleted list = %+v, want one entry with original url", deleted)
	}
}

// PurgeJob не должен трогать файлы, пока задание не скрыто.
func TestPurgeJob_RefusesVisibleJobBeforeTouchingFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	job, item := e.addFile(t, "keep.mp4")

	if err := e.svc.PurgeJob(ctx, job.ID); err == nil {
		t.Error("purge of a visible job must fail")
	}
	if _, err := os.Stat(item.Path); err != nil {
		t.Error("file must survive a refused purge")
	}

	if err := e.svc.HideJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.PurgeJob(ctx, job.ID); err != nil {
		t.Fatalf("purge hidden: %v", err)
	}
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Error("file must be removed together with the hidden job")
	}
}

// Постоянная ссылка должна учитывать BASE_PATH.
func TestCreateLink_IncludesBasePath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, item := e.addFile(t, "link.mp4")

	url, err := e.svc.CreateLink(ctx, item.ID)
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	const want = "https://media.example.com/talmor/f/"
	if len(url) <= len(want) || url[:len(want)] != want {
		t.Errorf("link = %q, want prefix %q", url, want)
	}

	// Ссылка постоянная: повторный вызов возвращает тот же адрес.
	again, _ := e.svc.CreateLink(ctx, item.ID)
	if again != url {
		t.Errorf("link changed between calls: %q vs %q", url, again)
	}
}

// Отзыв ссылки: прежний адрес перестаёт работать, следующий запрос выдаёт новый.
func TestRevokeLink(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, item := e.addFile(t, "revoke.mp4")

	first, err := e.svc.CreateLink(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := filepath.Base(first)

	if err := e.svc.RevokeLink(ctx, item.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := e.svc.ResolveToken(ctx, oldToken); err == nil {
		t.Error("отозванный токен не должен резолвиться")
	}

	second, err := e.svc.CreateLink(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Error("после отзыва должна выдаваться новая ссылка")
	}
	if _, err := e.svc.ResolveToken(ctx, filepath.Base(second)); err != nil {
		t.Errorf("новая ссылка не работает: %v", err)
	}
}

// Отзывать нечего — понятная ошибка, а не тихий успех.
func TestRevokeLink_WithoutLink(t *testing.T) {
	e := newEnv(t)
	_, item := e.addFile(t, "nolink.mp4")

	if err := e.svc.RevokeLink(context.Background(), item.ID); !errors.Is(err, library.ErrNoLink) {
		t.Errorf("ошибка = %v, ожидалась ErrNoLink", err)
	}
}

func TestResolveToken_RejectsDeletedItem(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, item := e.addFile(t, "gone.mp4")

	url, err := e.svc.CreateLink(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := filepath.Base(url)

	if _, err := e.svc.ResolveToken(ctx, token); err != nil {
		t.Fatalf("token must resolve while the file exists: %v", err)
	}
	if err := e.svc.DeleteItem(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveToken(ctx, token); err == nil {
		t.Error("token of a deleted item must not resolve")
	}
}

// Страница медиатеки должна сообщать полное число совпадений.
func TestMedia_PaginationReportsTotal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		e.addFile(t, name)
	}

	page, err := e.svc.Media(ctx, model.MediaFilter{Limit: 2})
	if err != nil {
		t.Fatalf("media: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("items = %d, want 2", len(page.Items))
	}
	if page.Total != 3 {
		t.Errorf("total = %d, want 3", page.Total)
	}

	next, err := e.svc.Media(ctx, model.MediaFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 {
		t.Errorf("second page items = %d, want 1", len(next.Items))
	}
	if next.Total != 3 {
		t.Errorf("second page total = %d, want 3", next.Total)
	}
}

func TestEnqueueExtractAudio_RejectsUnavailableItem(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, item := e.addFile(t, "audio-src.mp4")

	if err := e.svc.EnqueueExtractAudio(ctx, []string{item.ID}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if e.runner.enqueued != 1 {
		t.Errorf("runner enqueued %d times, want 1", e.runner.enqueued)
	}

	if err := e.svc.DeleteItem(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnqueueExtractAudio(ctx, []string{item.ID}); err == nil {
		t.Error("extracting audio from a deleted item must fail")
	}
}

// Пакетное извлечение: одна операция на весь набор, недоступные файлы отсеиваются.
func TestEnqueueExtractAudio_Bulk(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	_, first := e.addFile(t, "one.mp4")
	_, second := e.addFile(t, "two.mp4")
	_, gone := e.addFile(t, "gone.mp4")
	if err := e.svc.DeleteItem(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.EnqueueExtractAudio(ctx, []string{first.ID, gone.ID, second.ID}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	list, err := e.svc.Operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("операций = %d, ожидалась одна на весь набор", len(list))
	}
	if !strings.Contains(list[0].Title, "2 файлов") {
		t.Errorf("заголовок операции = %q, ожидалось упоминание двух файлов", list[0].Title)
	}
	if strings.Contains(list[0].Payload, gone.ID) {
		t.Error("удалённый файл не должен попадать в операцию")
	}
}

// Повторы в запросе не должны множить работу для ffmpeg.
func TestEnqueueExtractAudio_Deduplicates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, item := e.addFile(t, "dup.mp4")

	if err := e.svc.EnqueueExtractAudio(ctx, []string{item.ID, item.ID, "", item.ID}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	list, err := e.svc.Operations(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("операций = %d, err = %v", len(list), err)
	}
	if strings.Count(list[0].Payload, item.ID) != 1 {
		t.Errorf("идентификатор повторяется в задании операции: %s", list[0].Payload)
	}
	if !strings.Contains(list[0].Title, "dup.mp4") {
		t.Errorf("заголовок = %q, ожидалось имя единственного файла", list[0].Title)
	}
}

// Пустой набор — понятная ошибка, а не пустая операция в очереди.
func TestEnqueueExtractAudio_Empty(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.EnqueueExtractAudio(context.Background(), nil); !errors.Is(err, library.ErrNothingToDo) {
		t.Errorf("ошибка = %v, ожидалась ErrNothingToDo", err)
	}
}

// Отмена операции: запущенную прерываем через исполнителя, ожидающую — снимаем из очереди.
func TestCancelOperation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if err := e.svc.EnqueueBulkHide(ctx, []string{"job-1"}); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.Operations(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("operations = %v, err = %v", list, err)
	}

	// Исполнитель не подтверждает отмену — значит операция ещё не начата.
	e.runner.canCancel = false
	if err := e.svc.CancelOperation(ctx, list[0].ID); err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	list, _ = e.svc.Operations(ctx)
	if len(list) != 0 {
		t.Errorf("pending operation must be removed from the queue, got %d", len(list))
	}
}
