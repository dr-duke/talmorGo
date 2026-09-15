package golden

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// Отмена загрузки — сценарий, который трудно поймать: файл скачивается за
// секунду. Чтобы успеть нажать кнопку, тест сам замедляет загрузку штатным
// параметром yt-dlp (--limit-rate) через страницу настроек.

const slowRate = "--limit-rate 20K"

// setExtraArgs задаёт дополнительные аргументы загрузчика через настройки.
func setExtraArgs(t *testing.T, baseURL, args string) {
	t.Helper()
	form := url.Values{
		"yt_dlp_extra_args":    {args},
		"yt_dlp_proxy":         {""},
		"yt_dlp_output_format": {"mp4"},
		"yt_dlp_max_files":     {"100"},
		"yt_dlp_timeout":       {"300"},
		"lib_page_size":        {"200"},
	}
	resp, err := http.PostForm(baseURL+"/settings/runtime", form)
	if err != nil {
		t.Fatalf("настройки загрузчика: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("настройки загрузчика: код %d", resp.StatusCode)
	}
}

func TestCancelDuringDownload(t *testing.T) {
	baseURL := os.Getenv("TALMOR_URL")
	if baseURL == "" {
		t.Skip("TALMOR_URL не задан — сценарий требует запущенного экземпляра")
	}
	chrome := chromePath()
	if chrome == "" {
		t.Skip("браузер не найден (задайте CHROME_PATH)")
	}

	// Замедляем загрузку, чтобы задание успело побыть в статусе «скачивается».
	setExtraArgs(t, baseURL, slowRate)
	defer setExtraArgs(t, baseURL, "--no-warnings")

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
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("запуск браузера: %v", err)
	}

	s := &session{t: t, ctx: ctx, url: baseURL}

	s.step("медиатека открыта",
		chromedp.Navigate(baseURL+"/"),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID),
	)

	// Сценарий проверяет, что прерванная загрузка не оставила ни файла, ни записи,
	// поэтому посторонние строки в медиатеке сделали бы вывод бессмысленным.
	var existingRows int
	s.eval(`document.querySelectorAll('.media-row').length`, &existingRows)
	if existingRows > 0 {
		t.Skipf("в медиатеке уже %d строк — сценарию отмены нужен чистый экземпляр", existingRows)
	}
	s.step("ссылка поставлена в очередь",
		chromedp.SendKeys(`.header-add-form input[name="url"]`, testVideoURL, chromedp.ByQuery),
		chromedp.Click(`.header-add-form button[type="submit"]`, chromedp.ByQuery),
	)

	// Строка задания показывает «скачивается» — процесс yt-dlp уже работает.
	s.waitFor("задание перешло в «скачивается»",
		`!!document.querySelector('.media-row.status-running')`, 90*time.Second)

	// Кнопка отмены живёт в действиях строки. Жмём её через JS: во время
	// скачивания список перерисовывается по событиям сервера, и координатный
	// клик может уйти мимо только что заменённого узла.
	s.step("нажата отмена в интерфейсе",
		chromedp.Evaluate(`(() => {
			const btn = document.querySelector('.media-row.status-running [title="Отменить"]');
			if (!btn) throw new Error('кнопка отмены не найдена');
			btn.click();
			return true;
		})()`, nil),
	)

	s.waitFor("задание отменено",
		`!!document.querySelector('.media-row.status-cancelled')`, 30*time.Second)

	// Прерванная загрузка не должна оставить ни файла, ни записи о нём.
	var playable int
	s.eval(`document.querySelectorAll('.media-row [data-action="row-play"]').length`, &playable)
	if playable != 0 {
		t.Errorf("после отмены доступно к воспроизведению строк: %d, ожидалось 0", playable)
	}

	var title string
	s.eval(`document.querySelector('.media-row.status-cancelled .row-title-text')?.textContent || ''`, &title)
	if !strings.Contains(title, "test-videos") {
		t.Errorf("в отменённой строке ожидалась исходная ссылка, получено %q", title)
	}

	// Отменённое задание можно перезапустить — кнопка на месте.
	var hasRestart bool
	s.eval(`!!document.querySelector('.media-row.status-cancelled [title="Скачать повторно"], .media-row.status-cancelled [title="Перезапустить"]')`, &hasRestart)
	if !hasRestart {
		t.Error("у отменённого задания нет кнопки повторного скачивания")
	}
}
