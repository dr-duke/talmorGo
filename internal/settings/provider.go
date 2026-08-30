// Package settings собирает эффективную конфигурацию загрузчика: значения из
// окружения, перекрытые runtime-настройками из БД.
//
// Раньше эта сборка была продублирована в трёх местах (веб-обработчики, пул
// загрузок, Telegram-бот) и наборы полей разошлись: бот, например, не передавал
// yt-dlp ни куки, ни дополнительные аргументы. Здесь источник один.
package settings

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/repo"
)

// Ключи runtime-настроек в таблице settings.
const (
	KeyProxy        = "yt_dlp_proxy"
	KeyExtraArgs    = "yt_dlp_extra_args"
	KeyOutputFormat = "yt_dlp_output_format"
	KeyMaxFiles     = "yt_dlp_max_files"
	KeyTimeout      = "yt_dlp_timeout"
	KeyPageSize     = "lib_page_size"
)

// Keys — порядок полей в форме настроек.
var Keys = []string{KeyProxy, KeyExtraArgs, KeyOutputFormat, KeyMaxFiles, KeyTimeout, KeyPageSize}

// Provider отдаёт эффективные значения настроек, кешируя таблицу settings в памяти.
// Кеш инвалидируется при записи через сам Provider — единственный путь изменения.
type Provider struct {
	cfg  *config.Config
	repo repo.SettingsRepo

	mu     sync.RWMutex
	cache  map[string]string
	loaded bool
}

func New(cfg *config.Config, r repo.SettingsRepo) *Provider {
	return &Provider{cfg: cfg, repo: r}
}

// overrides возвращает актуальную карту перекрытий, подгружая её при первом обращении.
func (p *Provider) overrides(ctx context.Context) map[string]string {
	p.mu.RLock()
	if p.loaded {
		defer p.mu.RUnlock()
		return p.cache
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded {
		return p.cache
	}
	m := map[string]string{}
	if p.repo != nil {
		loaded, err := p.repo.All(ctx)
		if err != nil {
			slog.Error("settings: load overrides", "err", err)
		} else if loaded != nil {
			m = loaded
		}
	}
	p.cache = m
	p.loaded = true
	return p.cache
}

// Overrides возвращает копию сохранённых перекрытий (для формы настроек).
func (p *Provider) Overrides(ctx context.Context) map[string]string {
	src := p.overrides(ctx)
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Defaults возвращает значения из окружения — показываются как placeholder.
func (p *Provider) Defaults() map[string]string {
	return map[string]string{
		KeyProxy:        p.cfg.YtDlpProxy,
		KeyExtraArgs:    p.cfg.YtDlpExtraArgs,
		KeyOutputFormat: p.cfg.YtDlpOutputFormat,
		KeyMaxFiles:     strconv.Itoa(p.cfg.YtDlpMaxFilesPerRequest),
		KeyTimeout:      strconv.Itoa(p.cfg.YtDlpTimeout),
		KeyPageSize:     strconv.Itoa(p.cfg.LibPageSize),
	}
}

// Set сохраняет перекрытие (пустое значение удаляет его) и сбрасывает кеш.
func (p *Provider) Set(ctx context.Context, key, value string) error {
	if p.repo == nil {
		return nil
	}
	if err := p.repo.Set(ctx, key, strings.TrimSpace(value)); err != nil {
		return err
	}
	p.invalidate()
	return nil
}

func (p *Provider) invalidate() {
	p.mu.Lock()
	p.loaded = false
	p.cache = nil
	p.mu.Unlock()
}

func (p *Provider) str(ctx context.Context, key, fallback string) string {
	if v, ok := p.overrides(ctx)[key]; ok && v != "" {
		return v
	}
	return fallback
}

func (p *Provider) num(ctx context.Context, key string, fallback int) int {
	if v, ok := p.overrides(ctx)[key]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// PageSize — число строк медиатеки на странице.
func (p *Provider) PageSize(ctx context.Context) int {
	if n := p.num(ctx, KeyPageSize, p.cfg.LibPageSize); n > 0 {
		return n
	}
	return p.cfg.LibPageSize
}

// cookiesFile возвращает путь к объединённому файлу кук, если он существует.
func (p *Provider) cookiesFile() string {
	path := p.cfg.CookiesFilePath()
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func (p *Provider) extraArgs(ctx context.Context) []string {
	if v, ok := p.overrides(ctx)[KeyExtraArgs]; ok && v != "" {
		return strings.Fields(v)
	}
	return p.cfg.ExtraArgsList()
}

// DownloadOptions — параметры запуска yt-dlp для скачивания в указанный каталог.
func (p *Provider) DownloadOptions(ctx context.Context, outputDir string) downloader.Options {
	o := p.ProbeOptions(ctx)
	o.OutputDir = outputDir
	o.OutputFormat = p.str(ctx, KeyOutputFormat, p.cfg.YtDlpOutputFormat)
	return o
}

// ProbeOptions — параметры для проверки «плейлист или одиночное видео».
// Каталог и формат вывода при проверке не нужны.
func (p *Provider) ProbeOptions(ctx context.Context) downloader.Options {
	return downloader.Options{
		Binary:      p.cfg.YtDlpBinary,
		Proxy:       p.str(ctx, KeyProxy, p.cfg.YtDlpProxy),
		MaxFiles:    p.num(ctx, KeyMaxFiles, p.cfg.YtDlpMaxFilesPerRequest),
		Timeout:     time.Duration(p.num(ctx, KeyTimeout, p.cfg.YtDlpTimeout)) * time.Second,
		ExtraArgs:   p.extraArgs(ctx),
		CookiesFile: p.cookiesFile(),
	}
}
