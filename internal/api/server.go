package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/api/handler"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/playlist"
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
	tags repo.TagRepo,
	store *storage.Storage,
	pool handler.Enqueuer,
) *Server {
	basePath := strings.TrimRight(cfg.BasePath, "/")

	mux := http.NewServeMux()

	expander := playlist.New(jobs, tags)

	qh := &handler.QueueHandler{Jobs: jobs, Tags: tags, Pool: pool, Cfg: cfg, Expander: expander}
	mh := &handler.MediaHandler{
		Jobs: jobs, Files: files, Tags: tags,
		Tokens: tokens, Storage: store,
		BaseURL: cfg.BaseURL, Pool: pool, Cfg: cfg, Expander: expander,
	}
	lh := &handler.LinkHandler{Tokens: tokens, Files: files}

	// Статика.
	staticSub, _ := fs.Sub(web.StaticFiles, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Главная страница.
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		templ.Handler(templates.Index(basePath)).ServeHTTP(w, r)
	})

	// Медиатека — объединённый список (заменяет /queue + /files).
	mux.HandleFunc("GET /media", mh.Page)
	mux.HandleFunc("GET /media/list", mh.List)
	mux.HandleFunc("GET /files/deleted", mh.ListDeleted)
	mux.HandleFunc("GET /files/{id}/stream", mh.Stream)
	mux.HandleFunc("DELETE /files/{id}", mh.Delete)
	mux.HandleFunc("PATCH /files/{id}", mh.Rename)
	mux.HandleFunc("POST /files/{id}/link", mh.CreateLink)
	mux.HandleFunc("POST /jobs/{id}/redownload", mh.Redownload)
	mux.HandleFunc("POST /jobs/{id}/hide", mh.Hide)
	mux.HandleFunc("DELETE /jobs/{id}", mh.PurgeJob)
	mux.HandleFunc("POST /jobs/{id}/tags", mh.AddTag)
	mux.HandleFunc("DELETE /jobs/{id}/tags/{tag}", mh.RemoveTag)
	mux.HandleFunc("GET /jobs/{id}/log", mh.Log)

	// Очередь — добавление и управление.
	mux.HandleFunc("POST /queue", qh.Add)
	mux.HandleFunc("DELETE /queue/{id}", qh.Delete)
	mux.HandleFunc("POST /jobs/{id}/retry", qh.Retry)

	// Presigned link (публичный, без auth).
	mux.HandleFunc("GET /f/{token}", lh.Resolve)

	// Health.
	if cfg.HealthEndpoint != "" {
		mux.HandleFunc("GET "+cfg.HealthEndpoint, handler.Health)
	}

	var h http.Handler = mux
	if cfg.WebToken != "" {
		h = authMiddleware(cfg.WebToken, mux)
	}

	if basePath != "" {
		outer := http.NewServeMux()
		if cfg.HealthEndpoint != "" {
			outer.HandleFunc("GET "+cfg.HealthEndpoint, handler.Health)
		}
		outer.HandleFunc("GET "+basePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, basePath+"/", http.StatusMovedPermanently)
		})
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
