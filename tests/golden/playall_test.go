package golden

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// Сквозное воспроизведение: очередь берётся с сервера по текущему фильтру
// (а не из строк, загруженных в DOM), после окончания ролика включается следующий.
// Требует коллекции минимум с двумя видео — полоса «Воспроизвести всё» видна
// только при выбранной коллекции.
func TestPlayAllAdvancesToNextVideo(t *testing.T) {
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
		chromedp.Flag("mute-audio", true),
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

	s.step("выбрана коллекция",
		chromedp.Click(`.sidebar-nav-item[data-coll]`, chromedp.ByQuery),
		chromedp.Sleep(900*time.Millisecond),
	)
	var playAllVisible bool
	s.eval(`document.getElementById('play-all-bar').classList.contains('visible')`, &playAllVisible)
	if !playAllVisible {
		t.Fatal("полоса «Воспроизвести всё» не появилась для коллекции")
	}

	// Плейлист приходит с сервера: в нём только видео, аудио той же коллекции
	// в очередь воспроизведения попадать не должно.
	s.eval(`(window.__pl = null, fetch('library/playlist').then(r => r.json()).then(v => window.__pl = v), true)`, nil)
	s.waitFor("серверный плейлист получен", `Array.isArray(window.__pl)`, 15*time.Second)
	var playlistLen int
	s.eval(`window.__pl.length`, &playlistLen)
	if playlistLen < 2 {
		t.Fatalf("в плейлисте %d записей, для проверки перехода нужно минимум 2", playlistLen)
	}
	var onlyStreams bool
	s.eval(`window.__pl.every(e => typeof e.stream === 'string' && e.stream.includes('/stream') && typeof e.title === 'string')`, &onlyStreams)
	if !onlyStreams {
		t.Error("в плейлисте есть записи без потока или названия")
	}

	s.step("запущено воспроизведение всей коллекции",
		chromedp.Click(`[data-action="play-all"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#player-dialog[open]`, chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
	)

	var firstSrc string
	s.eval(`document.getElementById('main-player').getAttribute('src') || ''`, &firstSrc)
	if !strings.Contains(firstSrc, "/stream") {
		t.Fatalf("первое видео не запустилось: src = %q", firstSrc)
	}
	t.Logf("  играет первое: %s", firstSrc)

	// Сборки Chromium без проприетарных кодеков не проигрывают H.264, и ролик
	// никогда не досмотрится до конца. Тогда переход проверяем не ожиданием,
	// а событием окончания — это ровно та цепочка, за которую отвечает плеер.
	var canPlayH264 string
	s.eval(`document.createElement('video').canPlayType('video/mp4; codecs="avc1.42E01E"')`, &canPlayH264)

	if canPlayH264 == "" {
		t.Log("  браузер без H.264: досматриваем не ролик, а событие окончания")
		s.eval(`(document.getElementById('main-player').dispatchEvent(new Event('ended')), true)`, nil)
	} else {
		var playing bool
		s.eval(`document.getElementById('main-player').currentTime > 0`, &playing)
		if !playing {
			t.Error("видео открылось, но воспроизведение не началось")
		}
	}

	s.waitFor("включилось следующее видео очереди",
		`(document.getElementById('main-player').getAttribute('src') || '') !== `+quoteJS(firstSrc),
		60*time.Second)

	var secondSrc string
	s.eval(`document.getElementById('main-player').getAttribute('src') || ''`, &secondSrc)
	t.Logf("  переключилось на: %s", secondSrc)
	if !strings.Contains(secondSrc, "/stream") {
		t.Errorf("после перехода src = %q", secondSrc)
	}

	s.step("воспроизведение остановлено",
		chromedp.Click(`[data-action="player-close"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)
}

// quoteJS оборачивает строку в кавычки для подстановки в выражение страницы.
func quoteJS(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}
