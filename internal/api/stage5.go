package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"bshop/internal/service"
)

func (s *Server) stage5Routes(mux *http.ServeMux) {
	pg := func(r *http.Request) (string, string) { return r.URL.Query().Get("period"), r.URL.Query().Get("group") }

	// ---- admin: statistics ----
	mux.HandleFunc("GET /api/admin/stats/overview", s.admin(s.handle(func(r *http.Request) (any, error) {
		p, g := pg(r)
		return s.svc.StatsOverview(r.Context(), p, g)
	})))
	mux.HandleFunc("GET /api/admin/stats/economics", s.admin(s.handle(func(r *http.Request) (any, error) {
		m, _ := strconv.Atoi(r.URL.Query().Get("months"))
		return s.svc.StatsEconomics(r.Context(), m)
	})))
	mux.HandleFunc("GET /api/admin/stats/payments", s.admin(s.handle(func(r *http.Request) (any, error) {
		p, g := pg(r)
		return s.svc.StatsPayments(r.Context(), p, g)
	})))
	mux.HandleFunc("GET /api/admin/stats/referrals", s.admin(s.handle(func(r *http.Request) (any, error) {
		p, g := pg(r)
		return s.svc.StatsReferrals(r.Context(), p, g)
	})))
	mux.HandleFunc("GET /api/admin/stats/support", s.admin(s.handle(func(r *http.Request) (any, error) {
		p, g := pg(r)
		return s.svc.StatsSupport(r.Context(), p, g)
	})))
	mux.HandleFunc("GET /api/admin/stats/analytics", s.admin(s.handle(func(r *http.Request) (any, error) {
		p, g := pg(r)
		return s.svc.StatsAnalytics(r.Context(), p, g)
	})))
	mux.HandleFunc("GET /api/admin/stats/leads", s.admin(s.handle(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		return s.svc.StatsLeads(r.Context(), q.Get("temperature"), q.Get("q"), limit, offset)
	})))
	mux.HandleFunc("POST /api/admin/stats/leads/recompute", s.admin(s.handle(func(r *http.Request) (any, error) {
		n, err := s.svc.RecomputeLeads(r.Context())
		return map[string]int{"changed": n}, err
	})))

	// ---- Remnawave webhooks ----
	mux.HandleFunc("POST /api/webhooks/remnawave", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
			return
		}
		if err := s.svc.VerifyRemnawaveSignature(body, r.Header.Get("X-Remnawave-Signature")); err != nil {
			code := http.StatusUnauthorized
			if errors.Is(err, service.ErrWebhooksOff) {
				code = http.StatusServiceUnavailable
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		if err := s.svc.HandleRemnawaveWebhook(r.Context(), body); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/admin/webhooks", s.admin(s.handle(func(r *http.Request) (any, error) {
		return s.svc.WebhookLog(r.Context(), 50)
	})))
	mux.HandleFunc("POST /api/admin/webhooks/test", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		Event string `json:"event"`
	}) (any, error) {
		return nil, s.svc.TestCustomerWebhook(r.Context(), in.Event, customer(r))
	}))))

	// ---- admin: import from RWP (chunked upload → restore → dry run → apply) ----
	mux.HandleFunc("GET /api/admin/import/rwp", s.admin(s.handle(func(r *http.Request) (any, error) {
		return s.svc.ImportStatus(r.Context()), nil
	})))
	mux.HandleFunc("POST /api/admin/import/rwp/chunk", s.admin(s.handle(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		size, _ := strconv.ParseInt(q.Get("size"), 10, 64)
		offset, _ := strconv.ParseInt(q.Get("offset"), 10, 64)
		return s.svc.ImportChunk(q.Get("name"), size, offset, http.MaxBytesReader(nil, r.Body, 8<<20))
	})))
	mux.HandleFunc("POST /api/admin/import/rwp/bot", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		BotID int64 `json:"bot_id"`
	}) (any, error) {
		return s.svc.ImportRecheck(r.Context(), in.BotID)
	}))))
	mux.HandleFunc("POST /api/admin/import/rwp/apply", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		Wipe bool `json:"wipe"`
	}) (any, error) {
		return s.svc.ImportApply(r.Context(), in.Wipe)
	}))))
	mux.HandleFunc("POST /api/admin/import/rwp/cancel", s.admin(s.handle(func(r *http.Request) (any, error) {
		s.svc.ImportCancel(r.Context())
		return s.svc.ImportStatus(r.Context()), nil
	})))

	// ---- admin: sync customers from the Remnawave panel ----
	mux.HandleFunc("GET /api/admin/panel-sync", s.admin(s.handle(func(r *http.Request) (any, error) {
		return s.svc.PanelSyncStatus(), nil
	})))
	mux.HandleFunc("POST /api/admin/panel-sync", s.admin(s.handle(func(r *http.Request) (any, error) {
		return s.svc.StartPanelSync()
	})))
}
