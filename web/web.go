package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var StaticFiles embed.FS

type staticFile struct {
	content []byte
	etag    string
	ctype   string
}

// StaticHandler отдаёт встроенную статику с проверкой актуальности.
//
// http.FileServer поверх embed.FS не выставляет ни ETag, ни Last-Modified:
// у встроенных файлов нулевое время модификации. Без этих заголовков браузер
// кеширует файлы по своему усмотрению — Safari, например, продолжал отдавать
// стили и скрипты месячной давности после обновления приложения, и правки
// интерфейса до пользователя просто не доезжали.
//
// Поэтому считаем ETag по содержимому и просим браузер каждый раз
// перепроверять: ответ «не изменилось» стоит дёшево, устаревший скрипт — нет.
func StaticHandler() http.Handler {
	files := make(map[string]staticFile)

	sub, err := fs.Sub(StaticFiles, "static")
	if err != nil {
		slog.Error("static: подкаталог недоступен", "err", err)
		return http.NotFoundHandler()
	}

	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		content, readErr := fs.ReadFile(sub, p)
		if readErr != nil {
			slog.Error("static: читаем файл", "path", p, "err", readErr)
			return nil
		}
		sum := sha256.Sum256(content)
		ctype := mime.TypeByExtension(path.Ext(p))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		files[p] = staticFile{
			content: content,
			etag:    `"` + hex.EncodeToString(sum[:8]) + `"`,
			ctype:   ctype,
		}
		return nil
	})
	if err != nil {
		slog.Error("static: обход каталога", "err", err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("ETag", f.etag)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", f.ctype)

		if r.Header.Get("If-None-Match") == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(f.content) //nolint:errcheck
	})
}
