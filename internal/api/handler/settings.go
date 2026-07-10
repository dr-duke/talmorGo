package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/dr-duke/talmorGo/web/templates"
)

type SettingsHandler struct {
	Cookies  repo.CookieRepo
	Settings repo.SettingsRepo
	Jobs     repo.JobRepo
	Items    repo.ItemRepo
	Storage  *storage.Storage
	Cfg      *config.Config
	SiteName string
}

func (h *SettingsHandler) Page(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	records, err := h.Cookies.List(ctx)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	cf := h.Cfg.CookiesFilePath()
	fileStatus := cookieFileStatus(cf)
	rtSettings := h.loadRuntimeSettings(ctx)
	templ.Handler(templates.SettingsPage(h.Cfg.BasePath, h.SiteName, records, fileStatus, rtSettings, h.runtimeDefaults())).ServeHTTP(w, r)
}

// SaveRuntimeSettings сохраняет настройки загрузчика из формы.
func (h *SettingsHandler) SaveRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse form", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	keys := []string{"yt_dlp_proxy", "yt_dlp_extra_args", "yt_dlp_output_format", "yt_dlp_max_files", "yt_dlp_timeout"}
	for _, k := range keys {
		val := strings.TrimSpace(r.FormValue(k))
		if err := h.Settings.Set(ctx, k, val); err != nil {
			slog.Error("settings: save runtime setting", "key", k, "err", err)
		}
	}
	rtSettings := h.loadRuntimeSettings(ctx)
	templ.Handler(templates.RuntimeSettingsSection(h.Cfg.BasePath, rtSettings, h.runtimeDefaults())).ServeHTTP(w, r)
}

func (h *SettingsHandler) loadRuntimeSettings(ctx context.Context) map[string]string {
	if h.Settings == nil {
		return map[string]string{}
	}
	m, _ := h.Settings.All(ctx)
	if m == nil {
		return map[string]string{}
	}
	return m
}

// runtimeDefaults возвращает значения из конфига — показываются как placeholder в форме.
func (h *SettingsHandler) runtimeDefaults() map[string]string {
	return map[string]string{
		"yt_dlp_proxy":         h.Cfg.YtDlpProxy,
		"yt_dlp_extra_args":    h.Cfg.YtDlpExtraArgs,
		"yt_dlp_output_format": h.Cfg.YtDlpOutputFormat,
		"yt_dlp_max_files":     fmt.Sprintf("%d", h.Cfg.YtDlpMaxFilesPerRequest),
		"yt_dlp_timeout":       fmt.Sprintf("%d", h.Cfg.YtDlpTimeout),
	}
}

func cookieFileStatus(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("не найден (%s)", path)
	}
	return fmt.Sprintf("%s — %s", path, formatBytes(info.Size()))
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%d КБ", b>>10)
	default:
		return fmt.Sprintf("%d байт", b)
	}
}

// Import принимает Netscape-текст (весь cookies.txt), парсит по доменам и сохраняет.
func (h *SettingsHandler) Import(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse form", http.StatusBadRequest)
		return
	}
	raw := r.FormValue("body")
	if raw == "" {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	byDomain := parseCookiesByDomain(raw)
	ctx := r.Context()
	for domain, lines := range byDomain {
		if err := h.Cookies.Upsert(ctx, domain, strings.Join(lines, "\n")); err != nil {
			slog.Error("settings: upsert cookies", "domain", domain, "err", err)
		}
	}
	if err := h.rewriteFile(ctx); err != nil {
		slog.Error("settings: rewrite cookies file", "err", err)
	}

	records, _ := h.Cookies.List(ctx)
	templ.Handler(templates.CookieDomainList(records)).ServeHTTP(w, r)
}

// DeleteDomain удаляет куки домена.
func (h *SettingsHandler) DeleteDomain(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	if domain == "" {
		http.Error(w, "missing domain", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if err := h.Cookies.Delete(ctx, domain); err != nil {
		slog.Error("settings: delete cookies", "domain", domain, "err", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if err := h.rewriteFile(ctx); err != nil {
		slog.Error("settings: rewrite cookies file after delete", "err", err)
	}

	records, _ := h.Cookies.List(ctx)
	templ.Handler(templates.CookieDomainList(records)).ServeHTTP(w, r)
}

// rewriteFile пересоздаёт объединённый cookies.txt на диске.
func (h *SettingsHandler) rewriteFile(ctx context.Context) error {
	merged, err := h.Cookies.MergeAll(ctx)
	if err != nil {
		return err
	}
	path := h.Cfg.CookiesFilePath()
	if merged == "" {
		return os.Remove(path) // нет кук — файл не нужен (ошибка «не существует» ОК)
	}
	return os.WriteFile(path, []byte(merged), 0o600)
}

// Cleanup безвозвратно удаляет failed/hidden задания, их файлы с диска и из БД,
// а также записи потерянных файлов (lost_at IS NOT NULL).
func (h *SettingsHandler) Cleanup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	paths, err := h.Items.PathsForCleanup(ctx)
	if err != nil {
		slog.Error("settings: cleanup paths", "err", err)
	}
	for _, p := range paths {
		if delErr := h.Storage.Delete(p); delErr != nil {
			slog.Warn("settings: cleanup delete file", "path", p, "err", delErr)
		}
	}

	nJobs, err := h.Jobs.CleanupDead(ctx)
	if err != nil {
		slog.Error("settings: cleanup dead jobs", "err", err)
	}

	nFiles, err := h.Items.PruneLost(ctx)
	if err != nil {
		slog.Error("settings: prune lost files", "err", err)
	}

	msg := fmt.Sprintf("Удалено заданий: %d, файлов на диске: %d, записей потерянных файлов: %d",
		nJobs, len(paths), nFiles)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<p class="cleanup-result">%s</p>`, msg)
}

// parseCookiesByDomain группирует строки Netscape-файла по домену (первая колонка).
// Комментарии и пустые строки пропускаются.
func parseCookiesByDomain(raw string) map[string][]string {
	out := make(map[string][]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		col := strings.SplitN(line, "\t", 2)
		if len(col) < 2 {
			continue
		}
		domain := strings.TrimLeft(col[0], ".")
		if domain == "" {
			continue
		}
		out[domain] = append(out[domain], line)
	}
	return out
}
