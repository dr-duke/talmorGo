package main

import (
	"net/http"
	"testing"
)

// TestNewHTTPServer_Timeouts закрепляет таймауты сервера. Раньше он собирался
// прямо в main без единого ограничения, и медленный клиент занимал соединение
// бесконечно — до проверки доступа дело не доходило, поэтому отказ был
// достижим анонимно.
func TestNewHTTPServer_Timeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NewServeMux())

	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout не задан — соединение с недописанными заголовками живёт вечно")
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout не задан")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout не задан — простаивающие соединения копятся")
	}

	// Намеренно нулевой: общий таймаут записи оборвал бы SSE на /events и
	// раздачу больших видео. Если кто-то его выставит, тест должен упасть,
	// а не пользователь — на середине фильма.
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v; он оборвёт поток событий и раздачу видео", srv.WriteTimeout)
	}
}
