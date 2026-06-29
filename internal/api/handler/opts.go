package handler

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/repo"
)

// resolveExpanderOpts строит Options для Expander.ResolvePlaceholder,
// перекрывая значения конфига настройками из БД (если settingsRepo != nil).
// OutputDir и OutputFormat не нужны для проверки плейлиста — не заполняются.
func resolveExpanderOpts(ctx context.Context, cfg *config.Config, sr repo.SettingsRepo) downloader.Options {
	proxy := cfg.YtDlpProxy
	maxFiles := cfg.YtDlpMaxFilesPerRequest
	timeout := time.Duration(cfg.YtDlpTimeout) * time.Second

	if sr != nil {
		if v, _ := sr.Get(ctx, "yt_dlp_proxy"); v != "" {
			proxy = v
		}
		if v, _ := sr.Get(ctx, "yt_dlp_max_files"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				maxFiles = n
			}
		}
		if v, _ := sr.Get(ctx, "yt_dlp_timeout"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				timeout = time.Duration(n) * time.Second
			}
		}
	}

	cf := cfg.CookiesFilePath()
	if _, err := os.Stat(cf); err != nil {
		cf = ""
	}

	extraArgs := cfg.ExtraArgsList()
	if sr != nil {
		if v, _ := sr.Get(ctx, "yt_dlp_extra_args"); v != "" {
			extraArgs = strings.Fields(v)
		}
	}

	return downloader.Options{
		Binary:      cfg.YtDlpBinary,
		Proxy:       proxy,
		MaxFiles:    maxFiles,
		Timeout:     timeout,
		CookiesFile: cf,
		ExtraArgs:   extraArgs,
	}
}
