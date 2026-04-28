package middleware

import (
	"context"
	"net/http"
	"sync"
)

var sessionStore = struct {
	sync.RWMutex
	data map[string]int64
}{data: make(map[string]int64)}

func languageMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			cookie, err := r.Cookie("lang")
			if err == nil && (cookie.Value == "ru" || cookie.Value == "en") {
				lang = cookie.Value
			} else {
				lang = "en"
			}
		}
		http.SetCookie(w, &http.Cookie{
			Name:   "lang",
			Value:  lang,
			Path:   "/",
			MaxAge: 86400 * 365,
		})
		ctx := context.WithValue(r.Context(), "lang", lang)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		sessionStore.RLock()
		userID, ok := sessionStore.data[cookie.Value]
		sessionStore.RUnlock()

		if !ok || userID == 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		ctx := context.WithValue(r.Context(), "userID", userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
