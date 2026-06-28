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
	"github.com/dr-duke/talmorGo/web/templates"
)

type SettingsHandler struct {
	Cookies  repo.CookieRepo
	Cfg      *config.Config
	SiteName string
}

func (h *SettingsHandler) Page(w http.ResponseWriter, r *http.Request) {
	records, err := h.Cookies.List(r.Context())
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	cf := h.Cfg.CookiesFilePath()
	fileStatus := cookieFileStatus(cf)
	templ.Handler(templates.SettingsPage(h.Cfg.BasePath, h.SiteName, records, fileStatus)).ServeHTTP(w, r)
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
