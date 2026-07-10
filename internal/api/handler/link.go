package handler

import (
	"net/http"

	"github.com/dr-duke/talmorGo/internal/repo"
)

type LinkHandler struct {
	Tokens repo.TokenRepo
	Items  repo.ItemRepo
}

// Resolve отдаёт медиаэлемент по presigned-токену (публичный endpoint, без авторизации).
func (h *LinkHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	t, err := h.Tokens.GetByToken(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	item, err := h.Items.GetByID(r.Context(), t.ItemID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, item.Path)
}
