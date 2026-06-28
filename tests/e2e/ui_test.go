package e2e

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/dr-duke/talmorGo/internal/api"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shared Chrome: один процесс, один браузер, одна вкладка на все тесты.
// Каждый тест просто navigates в свой httptest.Server — изоляция через URL/порт.
// ─────────────────────────────────────────────────────────────────────────────

var tabCtx context.Context
var tabCancel context.CancelFunc

func TestMain(m *testing.M) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("allow-running-insecure-content", true),
		chromedp.Flag("unsafely-treat-insecure-origin-as-secure", "http://127.0.0.1"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	// Один браузер, одна вкладка — открываем сразу чтобы Chrome успел стартовать.
	tabCtx, tabCancel = chromedp.NewContext(allocCtx)
	defer tabCancel()

	if err := chromedp.Run(tabCtx, chromedp.Navigate("about:blank")); err != nil {
		panic("chromedp: init tab: " + err.Error())
	}

	os.Exit(m.Run())
}

// newTab возвращает shared вкладку с per-test таймаутом 60s.
// Все тесты используют одну и ту же вкладку — каждый navigates на свой URL.
func newTab(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(tabCtx, 60*time.Second)
	return ctx, cancel
}

// ─────────────────────────────────────────────────────────────────────────────
// Test environment
// ─────────────────────────────────────────────────────────────────────────────

type fakePool struct{}

func (p *fakePool) Enqueue()               {}
func (p *fakePool) CancelJob(string) bool  { return false }

type testEnv struct {
	URL     string
	Jobs    repo.JobRepo
	Files   repo.FileRepo
	Tokens  repo.TokenRepo
	Tags    repo.TagRepo
	DataDir string
	cleanup func()
}

func (e *testEnv) Close() { e.cleanup() }

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	tmpDir := t.TempDir()

	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}

	jobRepo := repo.NewJobRepo(database)
	fileRepo := repo.NewFileRepo(database)
	tokenRepo := repo.NewTokenRepo(database)
	tagRepo := repo.NewTagRepo(database)
	cookieRepo := repo.NewCookieRepo(database)

	cfg := &config.Config{BaseURL: "", BasePath: ""}
	srv := api.New(cfg, jobRepo, fileRepo, tokenRepo, tagRepo, cookieRepo, storage.New(tmpDir), &fakePool{})
	ts := httptest.NewServer(srv.Handler())

	return &testEnv{
		URL:     ts.URL,
		Jobs:    jobRepo,
		Files:   fileRepo,
		Tokens:  tokenRepo,
		Tags:    tagRepo,
		DataDir: tmpDir,
		cleanup: func() { ts.Close(); database.Close() },
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Seed helpers
// ─────────────────────────────────────────────────────────────────────────────

func seedDone(t *testing.T, env *testEnv) (jobID, fileID string) {
	t.Helper()
	ctx := context.Background()

	fpath := filepath.Join(env.DataDir, fmt.Sprintf("video_%s.mp4", uuid.NewString()[:8]))
	if err := os.WriteFile(fpath, []byte("fake mp4"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	job := &model.Job{URL: "https://youtube.com/watch?v=test", Title: "Test Video.mp4", Status: model.JobDone, Source: "web"}
	if err := env.Jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	f := &model.File{JobID: job.ID, Path: fpath, Name: "Test Video.mp4", Size: 8}
	if err := env.Files.Create(ctx, f); err != nil {
		t.Fatalf("create file: %v", err)
	}
	job.FileID = f.ID
	if err := env.Jobs.Update(ctx, job); err != nil {
		t.Fatalf("update job.file_id: %v", err)
	}
	return job.ID, f.ID
}

func seedPending(t *testing.T, env *testEnv) string {
	t.Helper()
	job := &model.Job{URL: "https://youtube.com/watch?v=pending", Status: model.JobPending, Source: "web"}
	if err := env.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create pending job: %v", err)
	}
	return job.ID
}

func seedFailed(t *testing.T, env *testEnv) string {
	t.Helper()
	job := &model.Job{URL: "https://youtube.com/watch?v=fail", Status: model.JobFailed, Error: "download error", Source: "web"}
	if err := env.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create failed job: %v", err)
	}
	return job.ID
}

// ─────────────────────────────────────────────────────────────────────────────
// Common actions
// ─────────────────────────────────────────────────────────────────────────────

// openMedia загружает страницу и ждёт инициализации HTMX.
func openMedia(baseURL string) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.Navigate(baseURL + "/"),
		chromedp.WaitVisible(`#content`, chromedp.ByID),
		chromedp.Sleep(800 * time.Millisecond),
	}
}

// autoConfirm переопределяет confirm() → true перед действием.
var autoConfirm = chromedp.Evaluate(`window.confirm = function(){ return true; }`, nil)

// waitForSwap ждёт HTMX-обновления медиалиста.
func waitForSwap() chromedp.Action {
	return chromedp.Sleep(600 * time.Millisecond)
}

// openRowMenu открывает меню «⋮» в первой строке (вторичные действия живут там).
func openRowMenu() chromedp.Action {
	return chromedp.Tasks{
		chromedp.Click(`[title="Ещё"]`, chromedp.ByQuery),
		chromedp.Sleep(150 * time.Millisecond),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

// T01: Страница загружается.
func TestPageLoads(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()

	ctx, cancel := newTab(t)
	defer cancel()

	var title string
	if err := chromedp.Run(ctx, openMedia(env.URL), chromedp.Title(&title)); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if !strings.Contains(title, "TalmorGo") {
		t.Errorf("title = %q; want TalmorGo", title)
	}
}

// T02: Пустая медиатека показывает заглушку.
func TestEmptyState(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()

	ctx, cancel := newTab(t)
	defer cancel()

	var visible bool
	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.Evaluate(`document.querySelector('.m3-empty') !== null`, &visible),
	)
	if err != nil {
		t.Fatalf("check empty state: %v", err)
	}
	if !visible {
		t.Error("empty state not shown for empty library")
	}
}

// T03: Форма добавления очищается после submit.
func TestAddFormClearsAfterSubmit(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`input[name="url"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="url"]`, "https://youtube.com/watch?v=abc"),
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("submit form: %v", err)
	}
	var val string
	if err := chromedp.Run(ctx, chromedp.Value(`input[name="url"]`, &val, chromedp.ByQuery)); err != nil {
		t.Fatalf("get input: %v", err)
	}
	if val != "" {
		t.Errorf("input not cleared after submit; val=%q", val)
	}
}

// T04: После добавления URL медиалист обновляется (HX-Trigger: mediaRefresh).
func TestAddFormUpdatesMediaList(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`input[name="url"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="url"]`, "https://youtube.com/watch?v=xyz"),
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(700*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("add url: %v", err)
	}

	var count int
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('.media-row').length`, &count),
	)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count == 0 {
		t.Error("no media rows after adding URL — HX-Trigger mediaRefresh may be broken")
	}
}

// T05: Кнопка «Отменить» переводит pending-задачу в статус cancelled (мягкая отмена).
func TestCancelPendingJobChangesStatus(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedPending(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-pending`, chromedp.ByQuery),
		autoConfirm,
		chromedp.Click(`[title="Отменить"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	var pendingCount, cancelledCount int
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('.status-pending').length`, &pendingCount),
		chromedp.Evaluate(`document.querySelectorAll('.status-cancelled').length`, &cancelledCount),
	)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if pendingCount != 0 {
		t.Errorf("pending row still shown after cancel (count=%d)", pendingCount)
	}
	if cancelledCount == 0 {
		t.Error("no .status-cancelled row after cancel; expected soft-cancel to keep row")
	}
}

// T06: Кнопка «Повторить» (retry failed) меняет статус через HX-Trigger.
func TestRetryFailedJobChangesStatus(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedFailed(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	// failed — архивный статус, скрыт с главной: ждём появления в DOM и раскрываем фильтром.
	var revealed bool
	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitReady(`.status-failed`, chromedp.ByQuery),
		chromedp.Evaluate(`activateStatusFilter('failed'); true`, &revealed),
		chromedp.WaitVisible(`.status-failed`, chromedp.ByQuery),
		chromedp.Click(`[title="Повторить"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}

	var failedCount int
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('.status-failed').length`, &failedCount),
	)
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if failedCount != 0 {
		t.Errorf("failed row still visible after retry (count=%d) — HX-Trigger mediaRefresh not working", failedCount)
	}
}

// T07: Кнопка «Удалить файл» помечает файл удалённым и строка остаётся в статусе deleted.
func TestDeleteFileUpdatesRow(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		autoConfirm,
		openRowMenu(),
		chromedp.Click(`[title="Удалить файл"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}

	var doneCount, deletedCount int
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('.status-done').length`, &doneCount),
		chromedp.Evaluate(`document.querySelectorAll('.status-deleted').length`, &deletedCount),
	)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if doneCount != 0 {
		t.Errorf("status-done still shown after delete (count=%d)", doneCount)
	}
	// Строка должна остаться — мягкое удаление, ссылка сохраняется
	if deletedCount == 0 {
		t.Error("status-deleted row not shown after file delete; row should stay for redownload")
	}
}

// T08: Кнопка «Скачать повторно» сбрасывает job в pending.
func TestRedownloadSetsPending(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		autoConfirm,
		openRowMenu(),
		chromedp.Click(`[title="Скачать повторно"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("redownload: %v", err)
	}

	// После redownload job переходит в checking (async → pending).
	// Принимаем оба статуса как «в очереди».
	var inQueueCount int
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('.status-checking, .status-pending').length`, &inQueueCount),
	)
	if err != nil {
		t.Fatalf("count in-queue: %v", err)
	}
	if inQueueCount == 0 {
		t.Error("no checking/pending row after redownload; expected job back in queue")
	}
}

// T09: Кнопка «Лог скачивания» открывает диалог с содержимым.
func TestLogDialogOpensWithContent(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	jobID, _ := seedDone(t, env)

	if err := env.Jobs.SaveLog(context.Background(), jobID, "[yt-dlp] Downloading video\nDone."); err != nil {
		t.Fatalf("save log: %v", err)
	}

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		openRowMenu(),
		chromedp.Click(`[onclick*="openLog"]`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("open log dialog: %v", err)
	}

	var dialogOpen bool
	var logText string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('log-dialog').open`, &dialogOpen),
		chromedp.InnerHTML(`#log-content`, &logText),
	)
	if err != nil {
		t.Fatalf("read dialog state: %v", err)
	}
	if !dialogOpen {
		t.Error("log dialog not open after clicking terminal button")
	}
	if !strings.Contains(logText, "yt-dlp") {
		t.Errorf("log content = %q; want to contain 'yt-dlp'", logText)
	}
}

// T10: Кнопка закрытия лог-диалога работает.
func TestLogDialogCloses(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	jobID, _ := seedDone(t, env)
	env.Jobs.SaveLog(context.Background(), jobID, "log") //nolint:errcheck

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		openRowMenu(),
		chromedp.Click(`[onclick*="openLog"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Click(`#log-dialog .player-close`, chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("close dialog: %v", err)
	}

	var open bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('log-dialog').open`, &open)); err != nil {
		t.Fatalf("check dialog: %v", err)
	}
	if open {
		t.Error("log dialog still open after clicking close button")
	}
}

// T11: Клик по строке с файлом открывает плеер.
func TestClickRowOpensPlayer(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		// Кликаем по заголовку (некликабельная зона строки), а не по .media-info,
		// чей центр может попасть на чип-кнопку.
		chromedp.Click(`.status-done .media-title`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("click row: %v", err)
	}

	var open bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('player-dialog').open`, &open)); err != nil {
		t.Fatalf("check player: %v", err)
	}
	if !open {
		t.Error("player dialog did not open after clicking media row")
	}
}

// T12: Поиск фильтрует строки на клиенте.
func TestSearchFilterHidesRows(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()

	ctx := context.Background()
	job1 := &model.Job{URL: "https://youtube.com/1", Title: "Alpha Movie.mp4", Status: model.JobPending, Source: "web"}
	env.Jobs.Create(ctx, job1) //nolint:errcheck
	job2 := &model.Job{URL: "https://youtube.com/2", Title: "Beta Clip.mp4", Status: model.JobPending, Source: "web"}
	env.Jobs.Create(ctx, job2) //nolint:errcheck

	bCtx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(bCtx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.media-row`, chromedp.ByQuery),
		chromedp.SendKeys(`#media-search`, "Alpha"),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	var visibleCount int
	err = chromedp.Run(bCtx,
		chromedp.Evaluate(
			`Array.from(document.querySelectorAll('.media-row')).filter(r => r.style.display !== 'none').length`,
			&visibleCount,
		),
	)
	if err != nil {
		t.Fatalf("count visible: %v", err)
	}
	if visibleCount != 1 {
		t.Errorf("search 'Alpha': visible=%d, want 1", visibleCount)
	}
}

// T13: Плеер закрывается кнопкой.
func TestPlayerCloses(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		chromedp.Click(`.status-done .media-title`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Click(`.player-close`, chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("close player: %v", err)
	}

	var open bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('player-dialog').open`, &open)); err != nil {
		t.Fatalf("check player: %v", err)
	}
	if open {
		t.Error("player still open after clicking close")
	}
}

// T14: После удаления файла строка остаётся в статусе deleted с кнопкой «Скачать повторно».
// Кнопка redownload работает — статус переходит в pending.
func TestDeletedFileAllowsRedownload(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	// Шаг 1: удалить файл
	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		autoConfirm,
		openRowMenu(),
		chromedp.Click(`[title="Удалить файл"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}

	// Кнопка «Скачать повторно» должна быть на удалённой строке
	var hasRedownload bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelector('.status-deleted [title="Скачать повторно"]') !== null`,
		&hasRedownload,
	)); err != nil || !hasRedownload {
		t.Fatal("redownload button not found on .status-deleted row")
	}

	// deleted — архивный статус, скрыт с главной: раскрываем фильтром перед кликом.
	var revealed bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`activateStatusFilter('deleted'); true`, &revealed)); err != nil {
		t.Fatalf("reveal deleted: %v", err)
	}

	// Шаг 2: нажать «Скачать повторно» — должен перейти в pending
	err = chromedp.Run(ctx,
		autoConfirm,
		chromedp.Click(`.status-deleted [title="Скачать повторно"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("redownload from deleted: %v", err)
	}

	var inQueueCount int
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelectorAll('.status-checking, .status-pending').length`, &inQueueCount,
	)); err != nil || inQueueCount == 0 {
		t.Errorf("expected job back in queue after redownload from deleted, got %d", inQueueCount)
	}
}

// T15: Кнопка «Добавить тег» добавляет тег в строку через fetch + HTMX.
func TestAddTagAppearsInRow(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		// mock window.prompt перед кликом — addTag() вызывает prompt('Новый тег:')
		chromedp.Evaluate(`window.prompt = function() { return "e2eTag"; }`, nil),
		chromedp.Click(`[title="Добавить тег"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}

	var tagVisible bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`Array.from(document.querySelectorAll('.m3-chip-sm')).some(el => el.textContent.includes('e2eTag'))`,
		&tagVisible,
	)); err != nil || !tagVisible {
		t.Error("added tag 'e2eTag' not visible in media row after HTMX swap")
	}
}

// T16: Кнопка «Убрать тег» (✕) удаляет тег из строки через HTMX.
func TestRemoveTagUpdatesRow(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	jobID, _ := seedDone(t, env)

	ctx0 := context.Background()
	tag, err := env.Tags.Upsert(ctx0, "RemoveMe")
	if err != nil {
		t.Fatalf("upsert tag: %v", err)
	}
	if err := env.Tags.AddToJob(ctx0, jobID, tag.ID); err != nil {
		t.Fatalf("add tag to job: %v", err)
	}

	ctx, cancel := newTab(t)
	defer cancel()

	err = chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.m3-chip-sm`, chromedp.ByQuery),
		chromedp.Click(`.chip-remove`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("remove tag: %v", err)
	}

	// Считаем только пользовательские теги; статус-чип (.m3-chip-status) не считается.
	var chipCount int
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelectorAll('.m3-chip-sm:not(.m3-chip-status)').length`, &chipCount,
	)); err != nil || chipCount != 0 {
		t.Errorf("expected no user tag chips after remove, got %d", chipCount)
	}
}

// T17: Кнопка «Переименовать» обновляет название файла через fetch + HTMX.
func TestRenameFileUpdatesTitle(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		// mock window.prompt — renamePrompt() вызывает prompt('Новое имя:', name)
		chromedp.Evaluate(`window.prompt = function() { return "Renamed Video.mp4"; }`, nil),
		openRowMenu(),
		chromedp.Click(`[title="Переименовать"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}

	var titleText string
	if err := chromedp.Run(ctx, chromedp.InnerHTML(`.media-title`, &titleText, chromedp.ByQuery)); err != nil {
		t.Fatalf("get title: %v", err)
	}
	if !strings.Contains(titleText, "Renamed Video.mp4") {
		t.Errorf("title after rename = %q; want 'Renamed Video.mp4'", titleText)
	}
}

// T18: Фильтр по тег-чипу в панели тегов скрывает неподходящие строки.
func TestTagFilterChipFilters(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()

	ctx0 := context.Background()
	job1 := &model.Job{URL: "https://youtube.com/watch?v=chip1", Title: "Alpha.mp4", Status: model.JobPending, Source: "web"}
	job2 := &model.Job{URL: "https://youtube.com/watch?v=chip2", Title: "Beta.mp4", Status: model.JobPending, Source: "web"}
	if err := env.Jobs.Create(ctx0, job1); err != nil {
		t.Fatalf("create job1: %v", err)
	}
	if err := env.Jobs.Create(ctx0, job2); err != nil {
		t.Fatalf("create job2: %v", err)
	}
	tagA, _ := env.Tags.Upsert(ctx0, "TagAlpha")
	tagB, _ := env.Tags.Upsert(ctx0, "TagBeta")
	env.Tags.AddToJob(ctx0, job1.ID, tagA.ID) //nolint:errcheck
	env.Tags.AddToJob(ctx0, job2.ID, tagB.ID) //nolint:errcheck

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.media-row`, chromedp.ByQuery),
		chromedp.Click(`[data-tag="TagAlpha"]`, chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("filter by tag: %v", err)
	}

	var visibleCount int
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`Array.from(document.querySelectorAll('.media-row')).filter(r => r.style.display !== 'none').length`,
		&visibleCount,
	)); err != nil {
		t.Fatalf("count visible: %v", err)
	}
	if visibleCount != 1 {
		t.Errorf("tag filter 'TagAlpha': visible=%d, want 1", visibleCount)
	}
}

// T20: Отменённая задача показывает кнопку «Скачать повторно» — lifecycle = cancelled.
func TestCancelledJobAllowsRedownload(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedPending(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	// Отменяем задачу
	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-pending`, chromedp.ByQuery),
		autoConfirm,
		chromedp.Click(`[title="Отменить"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// Кнопка «Скачать повторно» должна быть на cancelled строке
	var hasRedownload bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelector('.status-cancelled [title="Скачать повторно"]') !== null`,
		&hasRedownload,
	)); err != nil || !hasRedownload {
		t.Fatal("redownload button not found on .status-cancelled row")
	}

	// cancelled — архивный статус, скрыт с главной: раскрываем фильтром перед кликом.
	var revealed bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`activateStatusFilter('cancelled'); true`, &revealed)); err != nil {
		t.Fatalf("reveal cancelled: %v", err)
	}

	// Нажимаем — должен перейти в pending
	err = chromedp.Run(ctx,
		autoConfirm,
		chromedp.Click(`.status-cancelled [title="Скачать повторно"]`, chromedp.ByQuery),
		waitForSwap(),
	)
	if err != nil {
		t.Fatalf("redownload from cancelled: %v", err)
	}

	var inQueueCount int
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelectorAll('.status-checking, .status-pending').length`, &inQueueCount,
	)); err != nil || inQueueCount == 0 {
		t.Errorf("expected job back in queue after redownload from cancelled, got %d", inQueueCount)
	}
}

// T19: Кнопка «Смотреть» (play_circle) в панели действий открывает плеер.
func TestPlayButtonInActionsOpensPlayer(t *testing.T) {
	env := newTestEnv(t)
	defer env.Close()
	seedDone(t, env)

	ctx, cancel := newTab(t)
	defer cancel()

	err := chromedp.Run(ctx,
		openMedia(env.URL),
		chromedp.WaitVisible(`.status-done`, chromedp.ByQuery),
		chromedp.Click(`[title="Смотреть"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("click play button: %v", err)
	}

	var open bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('player-dialog').open`, &open)); err != nil {
		t.Fatalf("check player: %v", err)
	}
	if !open {
		t.Error("player dialog did not open after clicking play button in actions")
	}
}
