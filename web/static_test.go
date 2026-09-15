package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dr-duke/talmorGo/web"
)

// Статика раздавалась без ETag и Last-Modified, и браузер кешировал её по
// своему усмотрению: Safari месяц отдавал старые стили и скрипты, из-за чего
// правки интерфейса не доходили до пользователя. Тест держит контракт.

func get(h http.Handler, path string, headers map[string]string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestStatic_SendsValidators(t *testing.T) {
	h := web.StaticHandler()

	resp := get(h, "/app.css", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код = %d, ожидался 200", resp.StatusCode)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("без ETag браузер не сможет проверить актуальность файла")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, ожидался no-cache", cc)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("Content-Type = %q, ожидался CSS", ct)
	}
}

// Повторный запрос с прежним ETag должен стоить один «не изменилось».
func TestStatic_NotModified(t *testing.T) {
	h := web.StaticHandler()

	first := get(h, "/app.css", nil)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("ETag не выставлен")
	}

	second := get(h, "/app.css", map[string]string{"If-None-Match": etag})
	if second.StatusCode != http.StatusNotModified {
		t.Errorf("код = %d, ожидался 304", second.StatusCode)
	}

	// Чужой ETag — отдаём содержимое заново.
	stale := get(h, "/app.css", map[string]string{"If-None-Match": `"устарел"`})
	if stale.StatusCode != http.StatusOK {
		t.Errorf("при несовпадении ETag код = %d, ожидался 200", stale.StatusCode)
	}
}

// ETag считается по содержимому, поэтому у разных файлов он разный.
func TestStatic_ETagPerFile(t *testing.T) {
	h := web.StaticHandler()

	css := get(h, "/app.css", nil).Header.Get("ETag")
	js := get(h, "/js/player.js", nil).Header.Get("ETag")
	if css == "" || js == "" {
		t.Fatal("ETag не выставлен")
	}
	if css == js {
		t.Error("у разных файлов совпал ETag — проверка актуальности сломается")
	}
}

// Модули грузятся только с правильным типом содержимого.
func TestStatic_JavaScriptType(t *testing.T) {
	resp := get(web.StaticHandler(), "/js/main.js", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, для ES-модуля нужен javascript", ct)
	}
}

func TestStatic_NotFound(t *testing.T) {
	if resp := get(web.StaticHandler(), "/нет-такого.css", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("код = %d, ожидался 404", resp.StatusCode)
	}
}
