package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/api/handler"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
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
	items repo.ItemRepo,
	tokens repo.TokenRepo,
	tags repo.TagRepo,
	cookies repo.CookieRepo,
	settings repo.SettingsRepo,
	collections repo.CollectionRepo,
	operations repo.OperationRepo,
	store *storage.Storage,
	pool handler.Enqueuer,
	opsWorker handler.OpsEnqueuer,
	hub *sse.Hub,
) *Server {
	basePath := strings.TrimRight(cfg.BasePath, "/")
	siteName := cfg.SiteName

	mux := http.NewServeMux()

	expander := playlist.New(jobs, tags)
	expander.Hub = hub

	qh := &handler.QueueHandler{Jobs: jobs, Tags: tags, Ops: operations, Pool: pool, Cfg: cfg, Settings: settings, Expander: expander}
	mh := &handler.MediaHandler{
		Jobs: jobs, Items: items, Tags: tags,
		Tokens: tokens, Storage: store,
		BaseURL: cfg.BaseURL, Pool: pool, Cfg: cfg, Settings: settings,
		Collections: collections, Expander: expander,
		Ops: operations, OpsWorker: opsWorker,
	}
	ch := &handler.CollectionHandler{Collections: collections}
	lh := &handler.LinkHandler{Tokens: tokens, Items: items}
	sh := &handler.SettingsHandler{Cookies: cookies, Settings: settings, Jobs: jobs, Items: items, Tags: tags, Storage: store, Cfg: cfg, SiteName: siteName}

	// Статика.
	staticSub, _ := fs.Sub(web.StaticFiles, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Главная страница — рендерим с коллекциями для сайдбара.
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		cols, _ := collections.List(r.Context())
		templ.Handler(templates.Index(basePath, siteName, cols)).ServeHTTP(w, r)
	})

	// Медиатека: сайдбар, items, теги.
	mux.HandleFunc("GET /library/sidebar", mh.LibrarySidebar)
	mux.HandleFunc("GET /library/items", mh.LibraryItems)
	mux.HandleFunc("GET /library/tags", mh.TagsFragment)

	// Items: стриминг, удаление, переименование, ссылки, аудио.
	mux.HandleFunc("GET /items/{id}/stream", mh.Stream)
	mux.HandleFunc("DELETE /items/{id}", mh.Delete)
	mux.HandleFunc("PATCH /items/{id}", mh.Rename)
	mux.HandleFunc("PATCH /items/{id}/meta", mh.UpdateMeta)
	mux.HandleFunc("POST /items/meta-bulk", mh.BulkMeta)
	mux.HandleFunc("POST /items/{id}/link", mh.CreateLink)
	mux.HandleFunc("POST /items/{id}/extract-audio", mh.ExtractAudio)
	mux.HandleFunc("GET /items/deleted", mh.ListDeleted)

	// Jobs: управление заданиями.
	mux.HandleFunc("POST /jobs/{id}/redownload", mh.Redownload)
	mux.HandleFunc("POST /jobs/{id}/hide", mh.Hide)
	mux.HandleFunc("POST /jobs/{id}/unhide", mh.Unhide)
	mux.HandleFunc("DELETE /jobs/{id}", mh.PurgeJob)
	mux.HandleFunc("POST /jobs/{id}/tags", mh.AddTag)
	mux.HandleFunc("DELETE /jobs/{id}/tags/{tag}", mh.RemoveTag)
	mux.HandleFunc("GET /jobs/{id}/log", mh.Log)
	mux.HandleFunc("POST /media/bulk-tag", mh.BulkTag)
	mux.HandleFunc("POST /media/bulk-hide", mh.BulkHide)

	// Коллекции.
	mux.HandleFunc("GET /collections", ch.Fragment)
	mux.HandleFunc("GET /collections/cards", ch.Cards)
	mux.HandleFunc("POST /collections", ch.Create)
	mux.HandleFunc("PATCH /collections/{id}", ch.Rename)
	mux.HandleFunc("DELETE /collections/{id}", ch.Delete)
	mux.HandleFunc("POST /collections/{id}/jobs", ch.AddJobs)

	// Очередь.
	mux.HandleFunc("POST /queue", qh.Add)
	mux.HandleFunc("DELETE /queue/{id}", qh.Delete)
	mux.HandleFunc("POST /queue/cancel-all", qh.CancelAll)
	mux.HandleFunc("GET /queue/items", qh.Items)
	mux.HandleFunc("POST /jobs/{id}/retry", qh.Retry)
	mux.HandleFunc("DELETE /operations/{id}", qh.DismissOp)

	// Настройки.
	mux.HandleFunc("GET /settings", sh.Page)
	mux.HandleFunc("POST /settings/cookies/import", sh.Import)
	mux.HandleFunc("DELETE /settings/cookies/{domain}", sh.DeleteDomain)
	mux.HandleFunc("POST /settings/cleanup", sh.Cleanup)
	mux.HandleFunc("POST /settings/reindex", sh.Reindex)
	mux.HandleFunc("POST /settings/runtime", sh.SaveRuntimeSettings)

	// SSE.
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch, unsub := hub.Subscribe()
		defer unsub()

		fmt.Fprint(w, "event: ping\ndata: ok\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ch:
				fmt.Fprint(w, "event: update\ndata: 1\n\n")
				flusher.Flush()
			}
		}
	})

	// Presigned link (публичный).
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
