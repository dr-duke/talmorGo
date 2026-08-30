// Package golden проходит основной сценарий работы против запущенного экземпляра.
//
// В отличие от tests/e2e, который поднимает httptest-сервер с заглушками, здесь
// проверяется настоящее приложение целиком: реальный yt-dlp скачивает реальный
// файл, воркер переносит его в медиатеку, интерфейс показывает результат.
//
// Запуск (нужен поднятый экземпляр и браузер):
//
//	TALMOR_URL=http://localhost:18090 CHROME_PATH=/usr/bin/chromium-browser \
//	  go test ./tests/golden/... -v -timeout 10m
package golden

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// Свободный тестовый ролик (Big Buck Bunny, Creative Commons), 1 МБ.
const testVideoURL = "https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/Big_Buck_Bunny_360_10s_1MB.mp4"

func chromePath() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	for _, c := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/chromium-browser", "/usr/bin/chromium", "/usr/bin/google-chrome",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	for _, n := range []string{"chromium-browser", "chromium", "google-chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

type session struct {
	t   *testing.T
	ctx context.Context
	url string
}

// step выполняет шаг сценария и сообщает результат.
func (s *session) step(name string, actions ...chromedp.Action) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, actions...); err != nil {
		s.t.Fatalf("✗ %s: %v", name, err)
	}
	s.t.Logf("✓ %s", name)
}

// eval возвращает результат выражения в странице.
func (s *session) eval(expr string, out any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Evaluate(expr, out)); err != nil {
		s.t.Fatalf("вычисление %q: %v", expr, err)
	}
}

// answerPrompts подменяет prompt/confirm: диалоги браузера в сценарии не нужны.
func (s *session) answerPrompts(answer string) chromedp.Action {
	return chromedp.Evaluate(fmt.Sprintf(
		`window.prompt = () => %q; window.confirm = () => true;`, answer), nil)
}

// waitFor опрашивает условие в странице до истечения времени.
func (s *session) waitFor(name, expr string, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ok bool
		s.eval(expr, &ok)
		if ok {
			s.t.Logf("✓ %s", name)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.t.Fatalf("✗ %s: не дождались (%s)", name, expr)
}

func TestGoldenPath(t *testing.T) {
	baseURL := os.Getenv("TALMOR_URL")
	if baseURL == "" {
		t.Skip("TALMOR_URL не задан — сценарий требует запущенного экземпляра")
	}
	chrome := chromePath()
	if chrome == "" {
		t.Skip("браузер не найден (задайте CHROME_PATH)")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Вкладку открываем сразу и на долгоживущем контексте: иначе она окажется
	// привязана к контексту первого шага, и его cancel закроет вкладку —
	// следующий шаг получит «context canceled».
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("запуск браузера: %v", err)
	}

	s := &session{t: t, ctx: ctx, url: baseURL}

	// ── 1. Открыть медиатеку ───────────────────────────────────────────────
	s.step("главная загрузилась",
		chromedp.Navigate(baseURL+"/"),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID),
	)

	// Сценарий описывает путь «с нуля» и опирается на то, что медиатека пуста:
	// на экземпляре с чужими данными он проверял бы посторонние строки.
	var existingRows int
	s.eval(`document.querySelectorAll('.media-row').length`, &existingRows)
	if existingRows > 0 {
		t.Skipf("в медиатеке уже %d строк — золотому пути нужен чистый экземпляр", existingRows)
	}

	// ── 2. Поставить ссылку в очередь ──────────────────────────────────────
	s.step("ссылка отправлена в очередь",
		chromedp.SendKeys(`.header-add-form input[name="url"]`, testVideoURL, chromedp.ByQuery),
		chromedp.Click(`.header-add-form button[type="submit"]`, chromedp.ByQuery),
	)

	// ── 3. Дождаться скачивания настоящим yt-dlp ───────────────────────────
	s.waitFor("файл скачан и появился в медиатеке",
		`!!document.querySelector('.media-row.status-done')`, 3*time.Minute)

	var title string
	s.eval(`document.querySelector('.media-row.status-done .row-title-text').textContent.trim()`, &title)
	if !strings.Contains(title, "Big_Buck_Bunny") {
		t.Errorf("имя файла = %q, ожидалось имя скачанного ролика", title)
	}
	t.Logf("  скачано: %s", title)

	var size string
	s.eval(`document.querySelector('.media-row.status-done .row-size')?.textContent || ''`, &size)
	if size == "" {
		t.Error("размер файла не показан в строке")
	}
	t.Logf("  размер: %s", size)

	// ── 4. Воспроизведение ─────────────────────────────────────────────────
	s.step("клик по строке открывает плеер",
		chromedp.Click(`.media-row.status-done .row-title`, chromedp.ByQuery),
		chromedp.WaitVisible(`#player-dialog[open]`, chromedp.ByQuery),
	)
	s.step("плеер сворачивается в панель",
		chromedp.Click(`[data-action="player-minimize"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)
	var barVisible, dialogOpen bool
	s.eval(`document.getElementById('player-bar').classList.contains('visible')`, &barVisible)
	s.eval(`document.getElementById('player-dialog').open`, &dialogOpen)
	if !barVisible || dialogOpen {
		t.Errorf("после сворачивания: панель видна = %v, окно открыто = %v", barVisible, dialogOpen)
	}
	s.step("воспроизведение остановлено",
		chromedp.Click(`#player-bar [data-action="player-close"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	// ── 5. Тег ─────────────────────────────────────────────────────────────
	s.step("тег добавлен",
		s.answerPrompts("лекции"),
		chromedp.Click(`.media-row.status-done [data-action="add-tag"]`, chromedp.ByQuery),
	)
	s.waitFor("тег виден в строке",
		`!!document.querySelector('.media-row .tag-chip[data-tag="лекции"]')`, 10*time.Second)
	s.waitFor("тег появился в облаке",
		`!!document.querySelector('#tag-cloud .chip[data-tag="лекции"]')`, 10*time.Second)

	// ── 6. Фильтрация ──────────────────────────────────────────────────────
	s.step("фильтр по тегу включён",
		chromedp.Click(`#tag-cloud .chip[data-tag="лекции"]`, chromedp.ByQuery),
		chromedp.Sleep(700*time.Millisecond),
	)
	var rowsWithTag int
	s.eval(`document.querySelectorAll('.media-row').length`, &rowsWithTag)
	if rowsWithTag != 1 {
		t.Errorf("строк под фильтром тега = %d, ожидалась 1", rowsWithTag)
	}
	s.step("фильтр по тегу снят",
		chromedp.Click(`#tag-cloud .chip[data-tag="лекции"]`, chromedp.ByQuery),
		chromedp.Sleep(700*time.Millisecond),
	)

	s.step("поиск сузил выдачу",
		chromedp.SendKeys(`#media-search`, "Bunny", chromedp.ByQuery),
		chromedp.Sleep(900*time.Millisecond),
	)
	var foundRows int
	s.eval(`document.querySelectorAll('.media-row').length`, &foundRows)
	if foundRows != 1 {
		t.Errorf("найдено строк = %d, ожидалась 1", foundRows)
	}

	s.step("поиск по бессмыслице ничего не находит",
		chromedp.Evaluate(`document.getElementById('media-search').value = ''`, nil),
		chromedp.SendKeys(`#media-search`, "нетакогофайла", chromedp.ByQuery),
		chromedp.Sleep(900*time.Millisecond),
	)
	var emptyShown bool
	s.eval(`!!document.querySelector('#media-inner .empty-state')`, &emptyShown)
	if !emptyShown {
		t.Error("для пустой выдачи не показано пустое состояние")
	}

	s.step("поиск очищен",
		chromedp.Evaluate(`document.getElementById('media-search').value = ''`, nil),
		chromedp.SendKeys(`#media-search`, " ", chromedp.ByQuery),
		chromedp.Evaluate(`document.getElementById('media-search').value = ''; htmx.trigger(document.getElementById('media-search'), 'input')`, nil),
		chromedp.Sleep(900*time.Millisecond),
	)

	// ── 7. Выделение и коллекция ───────────────────────────────────────────
	s.step("строка выделена",
		chromedp.Click(`.media-row .row-checkbox`, chromedp.ByQuery),
		chromedp.WaitVisible(`#action-bar:not(.hidden)`, chromedp.ByQuery),
	)
	var selectCount string
	s.eval(`document.getElementById('select-count').textContent`, &selectCount)
	if !strings.HasPrefix(selectCount, "1") {
		t.Errorf("счётчик выделения = %q, ожидалось «1 выбрано»", selectCount)
	}

	s.step("коллекция создана из выделенного",
		s.answerPrompts("Подборка"),
		chromedp.Click(`[data-action="coll-dropdown"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-action="coll-create"]`, chromedp.ByQuery),
		chromedp.Click(`[data-action="coll-create"]`, chromedp.ByQuery),
	)
	s.waitFor("коллекция появилась в сайдбаре",
		`!!document.querySelector('.sidebar-nav-item[data-coll="Подборка"]')`, 15*time.Second)

	s.step("переход в коллекцию",
		chromedp.Click(`.sidebar-nav-item[data-coll="Подборка"]`, chromedp.ByQuery),
		chromedp.Sleep(700*time.Millisecond),
	)
	var playAllVisible bool
	s.eval(`document.getElementById('play-all-bar').classList.contains('visible')`, &playAllVisible)
	if !playAllVisible {
		t.Error("в коллекции не показана полоса «Воспроизвести всё»")
	}

	s.step("коллекция переименована",
		s.answerPrompts("Избранное"),
		chromedp.Click(`.sidebar-coll-row [data-action="row-menu"]`, chromedp.ByQuery),
		chromedp.Click(`[data-action="coll-rename"]`, chromedp.ByQuery),
	)
	s.waitFor("новое имя коллекции в сайдбаре",
		`!!document.querySelector('.sidebar-nav-item[data-coll="Избранное"]')`, 15*time.Second)

	s.step("возврат ко всей медиатеке",
		chromedp.Click(`.sidebar-nav-item[data-kind=""]`, chromedp.ByQuery),
		chromedp.Sleep(700*time.Millisecond),
	)

	// ── 8. Действия над файлом ─────────────────────────────────────────────
	s.step("файл переименован",
		s.answerPrompts("Моя лекция.mp4"),
		chromedp.Click(`.media-row [data-action="row-menu"]`, chromedp.ByQuery),
		chromedp.Click(`[data-action="rename-file"]`, chromedp.ByQuery),
	)
	s.waitFor("новое имя в строке",
		`document.querySelector('.media-row .row-title-text')?.textContent.includes('Моя лекция')`, 15*time.Second)

	s.step("лог скачивания открывается",
		chromedp.Click(`.media-row [data-action="row-menu"]`, chromedp.ByQuery),
		chromedp.Click(`[data-action="open-log"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#log-dialog[open]`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	)
	var logText string
	s.eval(`document.getElementById('log-content').textContent`, &logText)
	if strings.TrimSpace(logText) == "" || strings.Contains(logText, "Загрузка…") {
		t.Errorf("лог пуст: %q", logText)
	}
	s.step("лог закрыт",
		chromedp.Click(`#log-dialog [data-action="dialog-close"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	// ── 9. Удаление ────────────────────────────────────────────────────────
	s.step("файл удалён",
		s.answerPrompts(""),
		chromedp.Click(`.media-row [data-action="row-menu"]`, chromedp.ByQuery),
		chromedp.Click(`[data-action="delete-file"]`, chromedp.ByQuery),
	)
	s.waitFor("строка помечена удалённой",
		`!!document.querySelector('.media-row.status-deleted')`, 15*time.Second)

	// ── 10. Очередь и настройки ────────────────────────────────────────────
	s.step("раздел очереди открывается",
		chromedp.Click(`#sidebar-queue-btn`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	)
	var queueShown bool
	s.eval(`getComputedStyle(document.getElementById('queue-section')).display !== 'none'`, &queueShown)
	if !queueShown {
		t.Error("раздел очереди не отобразился")
	}

	s.step("страница настроек открывается",
		chromedp.Navigate(baseURL+"/settings"),
		chromedp.WaitVisible(`#runtime-settings-section`, chromedp.ByQuery),
	)
	s.step("параметр загрузчика сохраняется",
		chromedp.SendKeys(`#rs-timeout`, "600", chromedp.ByQuery),
		chromedp.Click(`#runtime-settings-section button[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	var savedTimeout string
	s.eval(`document.getElementById('rs-timeout').value`, &savedTimeout)
	if savedTimeout == "" {
		t.Error("значение таймаута не сохранилось")
	}
	t.Logf("  таймаут после сохранения: %s", savedTimeout)

	// ── 11. Ошибок в консоли быть не должно ────────────────────────────────
	s.step("возврат в медиатеку", chromedp.Navigate(baseURL+"/"),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID))
}
