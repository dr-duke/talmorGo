package handler

import (
	"net/http"

	"github.com/dr-duke/talmorGo/internal/library"
)

type LinkHandler struct {
	Lib *library.Service
}

// Resolve отдаёт файл по постоянной ссылке (публичный эндпоинт, без авторизации).
// Параметр download=true отдаёт файл вложением — на такие ссылки ведут кнопки
// «Скачать» в Telegram.
func (h *LinkHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	item, err := h.Lib.ResolveToken(r.Context(), r.PathValue("token"))
	if err != nil {
		notFoundOrGone(w, r, err)
		return
	}
	if r.URL.Query().Get("download") == "true" {
		setAttachment(w, item.Name)
	}
	http.ServeFile(w, r, item.Path)
}
