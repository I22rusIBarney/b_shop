// Package api exposes the JSON HTTP API used by the web app and webhooks.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"
)

type ctxKey int

const customerKey ctxKey = 1

type Server struct {
	svc     *service.Service
	log     *slog.Logger
	web     fs.FS // built frontend (may be nil in dev)
	limiter *limiter
	extra   []func(*http.ServeMux) // routes registered by other packages (bot webhook...)
}

func New(svc *service.Service, web fs.FS, log *slog.Logger) *Server {
	return &Server{svc: svc, log: log, web: web, limiter: newLimiter()}
}

// Mount lets other packages add routes.
func (s *Server) Mount(fn func(*http.ServeMux)) { s.extra = append(s.extra, fn) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	for _, fn := range s.extra {
		fn(mux)
	}
	return s.recoverer(mux)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ue *service.UserError
	switch {
	case errors.As(err, &ue):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ue.Msg})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "не найдено"})
	case errors.Is(err, service.ErrPaymentsDisabled):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, context.Canceled):
		return
	default:
		s.log.Error("api", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "внутренняя ошибка, попробуйте позже"})
	}
}

func bad(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return &service.UserError{Msg: "некорректный запрос"}
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, &service.UserError{Msg: "неверный идентификатор"}
	}
	return id, nil
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func customer(r *http.Request) *store.Customer {
	c, _ := r.Context().Value(customerKey).(*store.Customer)
	return c
}

// optionalAuth attaches the customer if a valid token is present.
func (s *Server) optionalAuth(r *http.Request) *http.Request {
	if tok := bearer(r); tok != "" {
		if c, err := s.svc.AuthenticateToken(r.Context(), tok); err == nil {
			return r.WithContext(context.WithValue(r.Context(), customerKey, c))
		}
	}
	return r
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r = s.optionalAuth(r)
		c := customer(r)
		if c == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "требуется вход"})
			return
		}
		if c.IsBlocked {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "доступ ограничен"})
			return
		}
		if s.svc.Settings.Get().General.MaintenanceMode && !c.IsAdmin {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "идут технические работы, загляните чуть позже"})
			return
		}
		h(w, r)
	}
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if !customer(r).IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "нет доступа"})
			return
		}
		s.setAvatarCookie(w, r, customer(r).ID)
		h(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "path", r.URL.Path, "panic", rec)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "внутренняя ошибка"})
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// ---- rate limiting (per key, fixed window) ----

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// allow returns false when key exceeded n hits within window.
func (l *limiter) allow(key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.hits[key]
	kept := h[:0]
	for _, t := range h {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= n {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 50000 {
		l.hits = map[string][]time.Time{}
	}
	return true
}

func (s *Server) limit(key string, n int, window time.Duration, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(key+"|"+clientIP(r), n, window) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "слишком много запросов, подождите немного"})
			return
		}
		h(w, r)
	}
}
