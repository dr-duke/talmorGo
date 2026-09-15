package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// serveTo отдаёт временный файл с указанным именем через serveMediaFile.
func serveTo(t *testing.T, name, body string) *http.Response {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	serveMediaFile(rec, req, path, name)
	return rec.Result()
}

// TestServeMediaFile_NeutralizesHTML проверяет, что файл с «исполняемым»
// расширением не отдаётся как страница. Тип определялся по расширению, а
// расширение задаёт пользователь: переименовав элемент в .html, можно было
// получить исполняемый документ на origin приложения — в том числе по
// публичной ссылке /f/{token}, где авторизации нет вовсе.
func TestServeMediaFile_NeutralizesHTML(t *testing.T) {
	for _, name := range []string{"evil.html", "evil.htm", "evil.svg", "evil.xhtml", "evil.js"} {
		t.Run(name, func(t *testing.T) {
			resp := serveTo(t, name, "<script>alert(document.domain)</script>")
			defer resp.Body.Close()

			if got := resp.Header.Get("Content-Type"); got != "application/octet-stream" {
				t.Errorf("Content-Type = %q; браузер исполнит такой ответ", got)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, ожидалось nosniff", got)
			}
		})
	}
}

// TestServeMediaFile_KeepsMediaTypes: воспроизведение ломать нельзя — у
// известных медиарасширений тип должен остаться правильным, иначе плеер и
// перемотка перестанут работать.
func TestServeMediaFile_KeepsMediaTypes(t *testing.T) {
	cases := map[string]string{
		"clip.mp4":  "video/mp4",
		"clip.webm": "video/webm",
		"clip.mkv":  "video/x-matroska",
		"track.m4a": "audio/mp4",
		"track.mp3": "audio/mpeg",
		"CLIP.MP4":  "video/mp4", // регистр расширения не должен влиять
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			resp := serveTo(t, name, "данные")
			defer resp.Body.Close()
			if got := resp.Header.Get("Content-Type"); got != want {
				t.Errorf("Content-Type = %q, ожидалось %q", got, want)
			}
		})
	}
}

// TestServeMediaFile_NoReferrer: токен постоянной ссылки не должен утекать в
// Referer вместе с запросами, которые инициирует отданное содержимое.
func TestServeMediaFile_NoReferrer(t *testing.T) {
	resp := serveTo(t, "clip.mp4", "данные")
	defer resp.Body.Close()
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, ожидалось no-referrer", got)
	}
}
