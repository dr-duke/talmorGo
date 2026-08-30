package golden

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// Сценарии, не покрытые основным золотым путём: фильтры по типу, массовое
// выделение, аудиоплеер, сквозное воспроизведение, перемотка, прямые ссылки,
// диалог ID3-тегов и раскрытие облака тегов.

// apiPost отправляет JSON-запрос к приложению.
func apiPost(t *testing.T, baseURL, path string, payload any) {
	t.Helper()
	var body *strings.Reader
	if payload != nil {
		blob, _ := json.Marshal(payload)
		body = strings.NewReader(string(blob))
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+path, body)
	if err != nil {
		t.Fatalf("запрос %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("запрос %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("запрос %s: код %d", path, resp.StatusCode)
	}
}

func TestRemainingScenarios(t *testing.T) {
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
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("запуск браузера: %v", err)
	}
	s := &session{t: t, ctx: ctx, url: baseURL}

	s.step("медиатека открыта",
		chromedp.Navigate(baseURL+"/"),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID),
	)

	// ── Фильтры по типу файла ──────────────────────────────────────────────
	s.step("вкладка «Видео»",
		chromedp.Click(`.sidebar-nav-item[data-kind="video"]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	var videoRows, audioRowsUnderVideo int
	s.eval(`document.querySelectorAll('.media-row').length`, &videoRows)
	s.eval(`document.querySelectorAll('.media-row[data-kind="audio"]').length`, &audioRowsUnderVideo)
	if videoRows == 0 || audioRowsUnderVideo != 0 {
		t.Errorf("вкладка «Видео»: строк %d, из них аудио %d", videoRows, audioRowsUnderVideo)
	}

	s.step("вкладка «Аудио»",
		chromedp.Click(`.sidebar-nav-item[data-kind="audio"]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	var audioRows, videoRowsUnderAudio int
	s.eval(`document.querySelectorAll('.media-row').length`, &audioRows)
	s.eval(`document.querySelectorAll('.media-row[data-kind="video"]').length`, &videoRowsUnderAudio)
	if audioRows == 0 || videoRowsUnderAudio != 0 {
		t.Errorf("вкладка «Аудио»: строк %d, из них видео %d", audioRows, videoRowsUnderAudio)
	}

	// ── Аудио играет в панели, без модального окна ─────────────────────────
	s.step("клик по аудио запускает воспроизведение",
		chromedp.Click(`.media-row[data-kind="audio"] .row-title`, chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
	)
	var barVisible, dialogOpen bool
	s.eval(`document.getElementById('player-bar').classList.contains('visible')`, &barVisible)
	s.eval(`document.getElementById('player-dialog').open`, &dialogOpen)
	if !barVisible {
		t.Error("панель плеера не появилась для аудио")
	}
	if dialogOpen {
		t.Error("для аудио не должно открываться модальное окно плеера")
	}
	var audioSrc string
	s.eval(`document.getElementById('audio-player').getAttribute('src') || ''`, &audioSrc)
	if !strings.Contains(audioSrc, "/stream") {
		t.Errorf("аудиоэлементу не задан поток: %q", audioSrc)
	}
	var kindIcon string
	s.eval(`document.getElementById('pb-kind-icon').textContent`, &kindIcon)
	if kindIcon != "audio_file" {
		t.Errorf("иконка в панели = %q, ожидалась audio_file", kindIcon)
	}

	s.step("воспроизведение остановлено",
		chromedp.Click(`#player-bar [data-action="player-close"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	// ── Выбрать все отображаемые ───────────────────────────────────────────
	s.step("возврат ко всем файлам",
		chromedp.Click(`.sidebar-nav-item[data-kind=""]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	s.step("выбраны все отображаемые",
		chromedp.Click(`[data-action="select-all"]`, chromedp.ByQuery),
		chromedp.Sleep(400*time.Millisecond),
	)
	var rowsTotal int
	var selectLabel string
	s.eval(`document.querySelectorAll('#media-inner .row-checkbox').length`, &rowsTotal)
	s.eval(`document.getElementById('select-count').textContent`, &selectLabel)
	if !strings.HasPrefix(selectLabel, fmt.Sprint(rowsTotal)) {
		t.Errorf("счётчик = %q при %d строках", selectLabel, rowsTotal)
	}

	// Кнопка извлечения аудио видна, раз в выделении есть видео.
	var extractShown bool
	s.eval(`getComputedStyle(document.getElementById('action-extract-btn')).display !== 'none'`, &extractShown)
	if !extractShown {
		t.Error("кнопка пакетного извлечения не появилась при выделении видео")
	}

	s.step("выделение снято",
		chromedp.Click(`[data-action="selection-clear"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	// ── Диалог ID3-тегов ───────────────────────────────────────────────────
	s.step("вкладка «Аудио» для правки тегов",
		chromedp.Click(`.sidebar-nav-item[data-kind="audio"]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	s.step("открыт диалог тегов",
		chromedp.Click(`.media-row [data-action="row-menu"]`, chromedp.ByQuery),
		chromedp.Click(`[data-action="meta-open-row"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#meta-dialog[open]`, chromedp.ByQuery),
	)
	s.step("поле «исполнитель» отмечено и заполнено",
		chromedp.Evaluate(`
			const row = document.querySelector('#meta-dialog .meta-row[data-field="artist"]');
			const check = row.querySelector('.meta-check');
			if (!check.checked) { check.checked = true; check.dispatchEvent(new Event('change', {bubbles:true})); }
			const input = document.getElementById('meta-artist');
			input.value = 'Проверочный Исполнитель';
		`, nil),
		chromedp.Click(`[data-action="meta-apply"]`, chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
	)
	s.waitFor("новый исполнитель виден в строке",
		`document.querySelector('.media-row[data-kind="audio"]')?.dataset.metaArtist === 'Проверочный Исполнитель'`,
		20*time.Second)

	// ── Прямые ссылки из меню строки ───────────────────────────────────────
	var openHref, downloadHref string
	s.eval(`document.querySelector('.media-row [data-action="row-menu"]') && (document.querySelector('.media-row [data-action="row-menu"]').click(), true)`, nil)
	s.eval(`document.querySelector('.row-menu a[target="_blank"]')?.getAttribute('href') || ''`, &openHref)
	s.eval(`[...document.querySelectorAll('.row-menu a')].find(a => a.href.includes('download=true'))?.getAttribute('href') || ''`, &downloadHref)
	if !strings.Contains(openHref, "/stream") {
		t.Errorf("ссылка «открыть» = %q", openHref)
	}
	if !strings.Contains(downloadHref, "download=true") {
		t.Errorf("ссылка «скачать» = %q", downloadHref)
	}

	// Ответ забираем опросом: chromedp.Evaluate не ждёт промис.
	s.eval(fmt.Sprintf(
		`(window.__linkStatus = {}, fetch(%q).then(r => window.__linkStatus.open = r.status), true)`,
		openHref), nil)
	s.eval(fmt.Sprintf(
		`(fetch(%q).then(r => window.__linkStatus.download = r.status), true)`,
		downloadHref), nil)
	s.waitFor("прямые ссылки отдают файл",
		`window.__linkStatus?.open === 200 && window.__linkStatus?.download === 200`,
		15*time.Second)

	// ── Перемотка в панели плеера ──────────────────────────────────────────
	s.step("возврат ко всем файлам",
		chromedp.Click(`.sidebar-nav-item[data-kind=""]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	s.step("открыто видео",
		chromedp.Click(`.media-row[data-kind="video"] .row-title`, chromedp.ByQuery),
		chromedp.WaitVisible(`#player-dialog[open]`, chromedp.ByQuery),
		chromedp.Sleep(2000*time.Millisecond),
	)
	s.step("плеер свёрнут в панель",
		chromedp.Click(`[data-action="player-minimize"]`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	)
	s.step("перемотка кликом по полосе",
		chromedp.Click(`#pb-track`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	var currentTime float64
	s.eval(`document.getElementById('main-player').currentTime`, &currentTime)
	if currentTime <= 0 {
		t.Errorf("после перемотки позиция = %v, ожидалась больше нуля", currentTime)
	}
	var fillWidth string
	s.eval(`document.getElementById('pb-fill').style.width`, &fillWidth)
	if fillWidth == "" || strings.HasPrefix(fillWidth, "0") {
		t.Errorf("полоса прогресса не отражает позицию: %q", fillWidth)
	}
	s.step("воспроизведение остановлено",
		chromedp.Click(`#player-bar [data-action="player-close"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	// ── Облако тегов: показываются первые пять ─────────────────────────────
	var jobID string
	s.eval(`document.querySelector('.media-row')?.dataset.jobId || ''`, &jobID)
	if jobID == "" {
		t.Fatal("не нашли задание для проставления тегов")
	}
	for i := 1; i <= 7; i++ {
		apiPost(t, baseURL, "/jobs/"+jobID+"/tags", map[string]string{
			"name": fmt.Sprintf("тег-%d", i),
		})
	}
	s.step("облако тегов обновлено",
		chromedp.Reload(),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID),
		chromedp.Sleep(1200*time.Millisecond),
	)

	var visibleChips, hiddenChips int
	s.eval(`[...document.querySelectorAll('#tag-cloud .chip')].filter(c => !c.classList.contains('tag-extra') && !c.classList.contains('tag-expand-btn')).length`, &visibleChips)
	s.eval(`document.querySelectorAll('#tag-cloud .chip.tag-extra').length`, &hiddenChips)
	if visibleChips != 5 {
		t.Errorf("в облаке видно %d тегов, ожидалось 5", visibleChips)
	}
	if hiddenChips == 0 {
		t.Error("остальные теги не спрятаны под кнопку «ещё»")
	}

	var expandVisible bool
	s.eval(`!!document.querySelector('#tag-cloud .tag-expand-btn')`, &expandVisible)
	if !expandVisible {
		t.Error("нет кнопки раскрытия облака тегов")
	}

	s.step("облако тегов раскрыто",
		chromedp.Click(`#tag-cloud .tag-expand-btn`, chromedp.ByQuery),
		chromedp.Sleep(400*time.Millisecond),
	)
	var expandedShown bool
	s.eval(`getComputedStyle(document.querySelector('#tag-cloud .chip.tag-extra')).display !== 'none'`, &expandedShown)
	if !expandedShown {
		t.Error("после раскрытия скрытые теги так и не показались")
	}
}
