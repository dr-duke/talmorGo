package golden

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/device"
)

// Окно плеера на телефоне в горизонтальной ориентации должно занимать экран
// целиком: раньше оно оставалось окошком min(96vw, 960px) с полями и шапкой.
//
// Нативный полноэкранный режим iOS (iosNative) здесь не проверяется — его умеет
// только Safari на устройстве; тест закрывает раскладку, которая от него не зависит.
func TestPlayerFillsScreenInLandscape(t *testing.T) {
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

	// Эмулируем настоящий телефон: полноэкранная раскладка включается только при
	// сенсорном вводе, поэтому одной подменой размеров окна её не проверить.
	s.step("телефон повёрнут горизонтально",
		chromedp.Emulate(device.IPhone13landscape),
		chromedp.Navigate(baseURL+"/"),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID),
	)

	s.step("открыто видео",
		chromedp.Click(`.media-row[data-kind="video"] .row-title`, chromedp.ByQuery),
		chromedp.WaitVisible(`#player-dialog[open]`, chromedp.ByQuery),
		chromedp.Sleep(1200*time.Millisecond),
	)

	var box string
	s.eval(`(() => {
		const d = document.getElementById('player-dialog');
		const r = d.getBoundingClientRect();
		const v = document.getElementById('main-player');
		const vr = v ? v.getBoundingClientRect() : {width:0,height:0};
		return JSON.stringify({
			w: Math.round(r.width), h: Math.round(r.height),
			vpW: window.innerWidth, vpH: window.innerHeight,
			videoH: Math.round(vr.height), videoW: Math.round(vr.width),
			radius: getComputedStyle(d).borderTopLeftRadius,
		});
	})()`, &box)
	t.Logf("  геометрия: %s", box)

	var dialogW, dialogH, vpW, vpH, videoH int
	s.eval(`Math.round(document.getElementById('player-dialog').getBoundingClientRect().width)`, &dialogW)
	s.eval(`Math.round(document.getElementById('player-dialog').getBoundingClientRect().height)`, &dialogH)
	s.eval(`window.innerWidth`, &vpW)
	s.eval(`window.innerHeight`, &vpH)
	s.eval(`Math.round(document.getElementById('main-player').getBoundingClientRect().height)`, &videoH)

	if dialogW < vpW {
		t.Errorf("ширина окна плеера %d при вьюпорте %d — экран занят не целиком", dialogW, vpW)
	}
	if dialogH < vpH {
		t.Errorf("высота окна плеера %d при вьюпорте %d — экран занят не целиком", dialogH, vpH)
	}
	// Видео должно вписываться в окно, а не выталкивать шапку за экран.
	if videoH > dialogH {
		t.Errorf("видео (%d) выше окна плеера (%d)", videoH, dialogH)
	}
	if videoH == 0 {
		t.Error("видео не занимает высоту")
	}

	// Кнопка полноэкранного режима должна быть в панели управления.
	var hasFullscreenBtn bool
	s.eval(`!!document.querySelector('#player-dialog [data-plyr="fullscreen"]')`, &hasFullscreenBtn)
	if !hasFullscreenBtn {
		t.Error("в плеере нет кнопки полноэкранного режима")
	}

	s.step("воспроизведение остановлено",
		chromedp.Click(`[data-action="player-close"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	// Вертикальная ориентация: окно остаётся окном с полями — на весь экран
	// разворачиваем только в горизонтальной, где это уместно.
	s.step("телефон повёрнут вертикально",
		chromedp.Emulate(device.IPhone13),
		chromedp.Navigate(baseURL+"/"),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID),
		chromedp.Click(`.media-row[data-kind="video"] .row-title`, chromedp.ByQuery),
		chromedp.WaitVisible(`#player-dialog[open]`, chromedp.ByQuery),
		chromedp.Sleep(900*time.Millisecond),
	)
	var portraitH, portraitVpH, portraitVideoH int
	s.eval(`Math.round(document.getElementById('player-dialog').getBoundingClientRect().height)`, &portraitH)
	s.eval(`window.innerHeight`, &portraitVpH)
	s.eval(`Math.round(document.getElementById('main-player').getBoundingClientRect().height)`, &portraitVideoH)
	t.Logf("  вертикально: окно %d при вьюпорте %d, видео %d", portraitH, portraitVpH, portraitVideoH)
	if portraitH >= portraitVpH {
		t.Errorf("вертикально окно заняло весь экран (%d из %d)", portraitH, portraitVpH)
	}
	if portraitVideoH == 0 {
		t.Error("вертикально видео не отображается")
	}
	s.step("воспроизведение остановлено",
		chromedp.Click(`[data-action="player-close"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	// На широком экране окно остаётся окном: правка не должна ломать десктоп.
	s.step("возврат к десктопному размеру",
		chromedp.Emulate(device.Reset),
		chromedp.EmulateViewport(1280, 900),
		chromedp.Navigate(baseURL+"/"),
		chromedp.WaitNotPresent(`#media-loading`, chromedp.ByID),
		chromedp.Click(`.media-row[data-kind="video"] .row-title`, chromedp.ByQuery),
		chromedp.WaitVisible(`#player-dialog[open]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	)
	var deskW, deskVpW int
	s.eval(`Math.round(document.getElementById('player-dialog').getBoundingClientRect().width)`, &deskW)
	s.eval(`window.innerWidth`, &deskVpW)
	if deskW >= deskVpW {
		t.Errorf("на десктопе окно плеера растянулось на весь экран (%d из %d)", deskW, deskVpW)
	}
	t.Logf("  на десктопе окно = %d при вьюпорте %d", deskW, deskVpW)
}
