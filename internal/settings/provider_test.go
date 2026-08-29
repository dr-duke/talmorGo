package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
)

func newProvider(t *testing.T, cfg *config.Config) (*settings.Provider, repo.SettingsRepo) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	r := repo.NewSettingsRepo(database)
	return settings.New(cfg, r), r
}

func baseCfg() *config.Config {
	return &config.Config{
		YtDlpBinary:             "/usr/bin/yt-dlp",
		YtDlpProxy:              "socks5://env:1080",
		YtDlpOutputFormat:       "mp4",
		YtDlpTimeout:            300,
		YtDlpMaxFilesPerRequest: 100,
		YtDlpExtraArgs:          "--no-warnings",
		LibPageSize:             200,
	}
}

func TestProvider_FallsBackToConfig(t *testing.T) {
	p, _ := newProvider(t, baseCfg())
	opts := p.DownloadOptions(context.Background(), "/staging")

	if opts.Proxy != "socks5://env:1080" {
		t.Errorf("proxy = %q, want env value", opts.Proxy)
	}
	if opts.OutputFormat != "mp4" {
		t.Errorf("format = %q", opts.OutputFormat)
	}
	if opts.Timeout != 300*time.Second {
		t.Errorf("timeout = %v", opts.Timeout)
	}
	if opts.MaxFiles != 100 {
		t.Errorf("max files = %d", opts.MaxFiles)
	}
	if len(opts.ExtraArgs) != 1 || opts.ExtraArgs[0] != "--no-warnings" {
		t.Errorf("extra args = %v", opts.ExtraArgs)
	}
	if opts.OutputDir != "/staging" {
		t.Errorf("output dir = %q", opts.OutputDir)
	}
}

func TestProvider_OverridesWinAndInvalidateCache(t *testing.T) {
	ctx := context.Background()
	p, _ := newProvider(t, baseCfg())

	// Прогреваем кеш значением из конфига.
	if got := p.ProbeOptions(ctx).Proxy; got != "socks5://env:1080" {
		t.Fatalf("proxy before override = %q", got)
	}

	if err := p.Set(ctx, settings.KeyProxy, "http://db:3128"); err != nil {
		t.Fatalf("set proxy: %v", err)
	}
	if err := p.Set(ctx, settings.KeyTimeout, "45"); err != nil {
		t.Fatalf("set timeout: %v", err)
	}

	opts := p.ProbeOptions(ctx)
	if opts.Proxy != "http://db:3128" {
		t.Errorf("proxy after override = %q (кеш не сброшен)", opts.Proxy)
	}
	if opts.Timeout != 45*time.Second {
		t.Errorf("timeout after override = %v", opts.Timeout)
	}

	// Пустое значение снимает перекрытие.
	if err := p.Set(ctx, settings.KeyProxy, ""); err != nil {
		t.Fatalf("clear proxy: %v", err)
	}
	if got := p.ProbeOptions(ctx).Proxy; got != "socks5://env:1080" {
		t.Errorf("proxy after clearing override = %q, want env value", got)
	}
}

// ProbeOptions должен нести куки и доп. аргументы: раньше бот собирал параметры
// сам и терял их, из-за чего приватные плейлисты не разворачивались.
func TestProvider_ProbeOptionsCarryCookiesAndExtraArgs(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.YtDlpOutputDir = dir

	p, _ := newProvider(t, cfg)
	ctx := context.Background()

	if got := p.ProbeOptions(ctx).CookiesFile; got != "" {
		t.Errorf("cookies file = %q, want empty when file absent", got)
	}

	cookies := cfg.CookiesFilePath()
	if err := os.WriteFile(cookies, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := p.ProbeOptions(ctx)
	if opts.CookiesFile != cookies {
		t.Errorf("cookies file = %q, want %q", opts.CookiesFile, cookies)
	}
	if len(opts.ExtraArgs) != 1 {
		t.Errorf("extra args = %v, want config value", opts.ExtraArgs)
	}
}

func TestProvider_PageSize(t *testing.T) {
	ctx := context.Background()
	p, _ := newProvider(t, baseCfg())

	if got := p.PageSize(ctx); got != 200 {
		t.Errorf("page size = %d, want 200", got)
	}
	if err := p.Set(ctx, settings.KeyPageSize, "50"); err != nil {
		t.Fatal(err)
	}
	if got := p.PageSize(ctx); got != 50 {
		t.Errorf("page size after override = %d, want 50", got)
	}
	// Мусор в значении не должен ронять выдачу.
	if err := p.Set(ctx, settings.KeyPageSize, "not-a-number"); err != nil {
		t.Fatal(err)
	}
	if got := p.PageSize(ctx); got != 200 {
		t.Errorf("page size with invalid override = %d, want config default", got)
	}
}
