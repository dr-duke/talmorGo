package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/api/handler"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/dr-duke/talmorGo/web"
	"github.com/dr-duke/talmorGo/web/templates"
)

type Server struct {
	cfg     *config.Config
	handler http.Handler
}

func New(
	cfg *config.Config,
	jobs repo.JobRepo,
	files repo.FileRepo,
	tokens repo.TokenRepo,
	store *storage.Storage,
	pool handler.Enqueuer,
) *Server {
	// basePath — нормализованный префикс без trailing slash (напр. "/talmor" или "").
	basePath := strings.TrimRight(cfg.BasePath, "/")

	mux := http.NewServeMux()

	qh := &handler.QueueHandler{Jobs: jobs, Pool: pool}
	fh := &handler.FilesHandler{Files: files, Tokens: tokens, Storage: store, BaseURL: cfg.BaseURL}
	lh := &handler.LinkHandler{Tokens: tokens, Files: files}

	// Статика.
	staticSub, _ := fs.Sub(web.StaticFiles, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Главная страница — передаём basePath шаблону для <base href>.
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		templ.Handler(templates.Index(basePath)).ServeHTTP(w, r)
	})

	// Queue
	mux.HandleFunc("GET /queue", qh.Page)
	mux.HandleFunc("GET /queue/list", qh.List)
	mux.HandleFunc("POST /queue", qh.Add)
	mux.HandleFunc("DELETE /queue/{id}", qh.Delete)
	mux.HandleFunc("POST /jobs/{id}/retry", qh.Retry)

	// Files
	mux.HandleFunc("GET /files", fh.Page)
	mux.HandleFunc("GET /files/list", fh.List)
	mux.HandleFunc("GET /files/deleted", fh.ListDeleted)
	mux.HandleFunc("GET /files/{id}/stream", fh.Stream)
	mux.HandleFunc("DELETE /files/{id}", fh.Delete)
	mux.HandleFunc("PATCH /files/{id}", fh.Rename)
	mux.HandleFunc("POST /files/{id}/link", fh.CreateLink)

	// Presigned link (публичный, без auth).
	mux.HandleFunc("GET /f/{token}", lh.Resolve)

	// Health
	if cfg.HealthEndpoint != "" {
		mux.HandleFunc("GET "+cfg.HealthEndpoint, handler.Health)
	}

	var h http.Handler = mux
	if cfg.WebToken != "" {
		h = authMiddleware(cfg.WebToken, mux)
	}

	// Если задан basePath, оборачиваем: внешний mux снимает префикс и отдаёт внутреннему.
	if basePath != "" {
		outer := http.NewServeMux()
		// Редирект /talmor → /talmor/
		outer.HandleFunc("GET "+basePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, basePath+"/", http.StatusMovedPermanently)
		})
		// Все запросы /talmor/* — снимаем prefix и передаём внутреннему handler.
		outer.Handle(basePath+"/", http.StripPrefix(basePath, h))
		h = outer
	}

	return &Server{cfg: cfg, handler: h}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Presigned-ссылки публичны.
		if strings.HasPrefix(r.URL.Path, "/f/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") == "Bearer "+token {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie("_auth"); err == nil && c.Value == token {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}
