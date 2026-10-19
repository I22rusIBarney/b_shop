package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bshop/internal/store"
)

// Admin avatars are <img> tags, which cannot send the Authorization header. Every admin API response sets a
// short-lived cookie "<admin id>.<HMAC>" scoped to /api/avatars; the session token itself never goes into a cookie.
const avatarCookie = "bshop_av"

func (s *Server) avatarSig(adminID int64) string {
	m := hmac.New(sha256.New, []byte(s.svc.Cfg.SecretKey))
	m.Write([]byte("admin-avatars:" + strconv.FormatInt(adminID, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) setAvatarCookie(w http.ResponseWriter, r *http.Request, adminID int64) {
	if c, err := r.Cookie(avatarCookie); err == nil && c.Value != "" {
		return // still valid; one Set-Cookie per day is enough
	}
	http.SetCookie(w, &http.Cookie{
		Name: avatarCookie, Value: strconv.FormatInt(adminID, 10) + "." + s.avatarSig(adminID),
		Path: "/api/avatars", MaxAge: int((12 * time.Hour).Seconds()), HttpOnly: true,
		Secure: !s.svc.Cfg.Dev, SameSite: http.SameSiteStrictMode,
	})
}

// avatarAdmin checks the cookie and that its owner is still an administrator.
func (s *Server) avatarAdmin(r *http.Request) bool {
	c, err := r.Cookie(avatarCookie)
	if err != nil {
		return false
	}
	idStr, sig, ok := strings.Cut(c.Value, ".")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if !ok || err != nil || !hmac.Equal([]byte(sig), []byte(s.avatarSig(id))) {
		return false
	}
	admin, err := store.GetCustomer(r.Context(), s.svc.Store.Pool, id)
	return err == nil && admin.IsAdmin && !admin.IsBlocked
}

func (s *Server) avatarRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/avatars/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.avatarAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data, err := s.svc.Avatar(r.Context(), id)
		if err != nil || len(data) == 0 {
			w.Header().Set("Cache-Control", "private, max-age=3600")
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(data)
	})
}
