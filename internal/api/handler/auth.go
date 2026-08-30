package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/web/templates"
)

// AuthCookie — имя cookie с токеном доступа.
const AuthCookie = "_auth"

// AuthHandler выдаёт cookie с токеном. Без него WEB_TOKEN закрывал интерфейс
// полностью: заголовок Authorization браузер сам не отправляет, а EventSource
// не умеет и этого.
type AuthHandler struct {
	Token    string
	BasePath string
	SiteName string
}

func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	templ.Handler(templates.Login(h.BasePath, h.SiteName, false)).ServeHTTP(w, r)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(r.FormValue("token"))
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.Token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		templ.Handler(templates.Login(h.BasePath, h.SiteName, true)).ServeHTTP(w, r)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     AuthCookie,
		Value:    h.Token,
		Path:     h.cookiePath(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   int((365 * 24 * 60 * 60)), // держим сессию год: сервис домашний
	})
	http.Redirect(w, r, h.cookiePath(), http.StatusSeeOther)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   AuthCookie,
		Value:  "",
		Path:   h.cookiePath(),
		MaxAge: -1,
	})
	http.Redirect(w, r, h.cookiePath()+"login", http.StatusSeeOther)
}

func (h *AuthHandler) cookiePath() string {
	if h.BasePath == "" {
		return "/"
	}
	return strings.TrimRight(h.BasePath, "/") + "/"
}
