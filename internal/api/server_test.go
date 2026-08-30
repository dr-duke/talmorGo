package api_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dr-duke/talmorGo/internal/api"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/library"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/queue"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
	"github.com/dr-duke/talmorGo/internal/sse"
	"github.com/dr-duke/talmorGo/internal/storage"
)

type stubPool struct{}

func (stubPool) Enqueue()              {}
func (stubPool) CancelJob(string) bool { return false }

type stubRunner struct{}

func (stubRunner) Enqueue()           {}
func (stubRunner) Cancel(string) bool { return false }

// newServer собирает приложение с указанными токеном и префиксом монтирования.
func newServer(t *testing.T, webToken, basePath string) http.Handler {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := &config.Config{
		WebToken: webToken, BasePath: basePath, SiteName: "TalmorGo",
		HealthEndpoint: "/health", YtDlpOutputDir: dir, LibPageSize: 50,
	}

	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	tags := repo.NewTagRepo(database)
	store := storage.New(dir)
	provider := settings.New(cfg, repo.NewSettingsRepo(database))

	return api.New(api.Deps{
		Cfg: cfg,
		Lib: &library.Service{
			Jobs: jobs, Items: items, Tags: tags,
			Tokens:      repo.NewTokenRepo(database),
			Collections: repo.NewCollectionRepo(database),
			Ops:         repo.NewOperationRepo(database),
			Storage:     store, Settings: provider, Cfg: cfg, Runner: stubRunner{},
		},
		Queue: &queue.Service{
			Jobs: jobs, Items: items, Storage: store,
			Expander: playlist.New(jobs, tags), Pool: stubPool{}, Settings: provider,
		},
		Settings: provider,
		Cookies:  repo.NewCookieRepo(database),
		Hub:      sse.New(),
	}).Handler()
}

// do выполняет запрос без следования редиректам.
func do(h http.Handler, method, path string, headers map[string]string) *http.Response {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

var htmlAccept = map[string]string{"Accept": "text/html,application/xhtml+xml"}

// Навигация без токена должна вести на форму входа внутри точки монтирования:
// относительный редирект уводил на /login мимо префикса, и там был 404.
func TestAuth_RedirectKeepsBasePath(t *testing.T) {
	h := newServer(t, "secret", "/talmor")

	resp := do(h, http.MethodGet, "/talmor/", htmlAccept)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/talmor/login" {
		t.Errorf("Location = %q, want /talmor/login", got)
	}

	// И сама форма по этому адресу должна отвечать.
	if resp := do(h, http.MethodGet, "/talmor/login", htmlAccept); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /talmor/login = %d, want 200", resp.StatusCode)
	}
}

func TestAuth_RedirectWithoutBasePath(t *testing.T) {
	h := newServer(t, "secret", "")
	resp := do(h, http.MethodGet, "/", htmlAccept)
	if got := resp.Header.Get("Location"); got != "/login" {
		t.Errorf("Location = %q, want /login", got)
	}
}

// Запросы данных получают 401, а не редирект: HTMX не должен подставлять
// страницу входа вместо фрагмента.
func TestAuth_DataRequestsGet401(t *testing.T) {
	h := newServer(t, "secret", "")
	resp := do(h, http.MethodGet, "/library/items", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_PublicPaths(t *testing.T) {
	h := newServer(t, "secret", "")
	cases := []struct {
		path string
		want int
	}{
		{"/login", http.StatusOK},
		{"/health", http.StatusOK},
		{"/static/js/main.js", http.StatusOK},
		{"/f/unknown-token", http.StatusNotFound}, // публичный, но токена нет
	}
	for _, c := range cases {
		if resp := do(h, http.MethodGet, c.path, nil); resp.StatusCode != c.want {
			t.Errorf("GET %s = %d, want %d", c.path, resp.StatusCode, c.want)
		}
	}
}

func TestAuth_AcceptsTokenAndCookie(t *testing.T) {
	h := newServer(t, "secret", "")

	resp := do(h, http.MethodGet, "/library/items", map[string]string{
		"Authorization": "Bearer secret",
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bearer: status = %d, want 200", resp.StatusCode)
	}

	req := httptest.NewRequest(http.MethodGet, "/library/items", nil)
	req.AddCookie(&http.Cookie{Name: "_auth", Value: "secret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie: status = %d, want 200", rec.Code)
	}
}

// Без WEB_TOKEN авторизация выключена полностью.
func TestAuth_DisabledWithoutToken(t *testing.T) {
	h := newServer(t, "", "")
	if resp := do(h, http.MethodGet, "/library/items", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// Пустые списки должны сериализоваться в [], иначе клиент падает на null.
func TestJSONEndpoints_ReturnEmptyArrays(t *testing.T) {
	h := newServer(t, "", "")
	for _, path := range []string{"/collections", "/items/deleted", "/library/playlist"} {
		resp := do(h, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", path, resp.StatusCode)
			continue
		}
		buf := make([]byte, 4)
		n, _ := resp.Body.Read(buf)
		if got := string(buf[:n]); got == "null" {
			t.Errorf("GET %s вернул null вместо []", path)
		}
	}
}
