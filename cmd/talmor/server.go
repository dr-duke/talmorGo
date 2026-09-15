package main

import (
	"net/http"
	"time"
)

// newHTTPServer собирает HTTP-сервер с таймаутами.
//
// Без них соединение с недописанными заголовками живёт бесконечно, а проверка
// доступа до этого места ещё не доходит — то есть отказ достижим анонимно, и
// WEB_TOKEN от него не защищает. Несколько тысяч таких соединений исчерпывают
// файловые дескрипторы процесса, после чего замолкают и веб, и поток событий.
//
// WriteTimeout сознательно не задан: он оборвал бы поток событий на /events и
// раздачу больших видео, которые идут часами. Ограничивать запись нужно
// точечно, а не общим таймаутом сервера.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
