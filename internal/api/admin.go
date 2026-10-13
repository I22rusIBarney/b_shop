package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"bshop/internal/service"
	"bshop/internal/settings"
	"bshop/internal/store"

	"github.com/jackc/pgx/v5"
)

var errorsAs = errors.As

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/settings", s.admin(s.adminSettingsList))
	mux.HandleFunc("GET /api/admin/settings/{section}", s.admin(s.adminSettingsGet))
	mux.HandleFunc("PUT /api/admin/settings/{section}", s.admin(s.adminSettingsPut))
	mux.HandleFunc("POST /api/admin/settings/{section}/test", s.admin(s.adminSettingsTest))

	mux.HandleFunc("GET /api/admin/plans", s.admin(s.adminPlans))
	mux.HandleFunc("POST /api/admin/plans", s.admin(s.adminPlanSave))
	mux.HandleFunc("PUT /api/admin/plans/{id}", s.admin(s.adminPlanSave))
	mux.HandleFunc("DELETE /api/admin/plans/{id}", s.admin(s.adminPlanDelete))
	mux.HandleFunc("POST /api/admin/plans/{id}/default", s.admin(s.adminPlanDefault))
	mux.HandleFunc("POST /api/admin/plans/reorder", s.admin(s.adminPlanReorder))
	mux.HandleFunc("GET /api/admin/remnawave/squads", s.admin(s.adminSquads))
	mux.HandleFunc("GET /api/admin/remnawave/subpage-configs", s.admin(func(w http.ResponseWriter, r *http.Request) {
		list, err := s.svc.Panel.SubpageConfigs(r.Context())
		if err != nil {
			s.fail(w, r, &service.UserError{Msg: "панель Remnawave недоступна: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, list)
	}))

	mux.HandleFunc("GET /api/admin/customers", s.admin(s.adminCustomers))
	mux.HandleFunc("GET /api/admin/customers/{id}", s.admin(s.adminCustomer))
	mux.HandleFunc("PATCH /api/admin/customers/{id}", s.admin(s.adminCustomerPatch))
	mux.HandleFunc("GET /api/admin/payments", s.admin(s.adminPayments))
	mux.HandleFunc("POST /api/admin/customers/{id}/subscriptions", s.admin(s.adminGrant))
	mux.HandleFunc("POST /api/admin/subscriptions/{id}/days", s.admin(s.adminDays))
	mux.HandleFunc("POST /api/admin/subscriptions/{id}/reset-traffic", s.admin(s.adminResetTraffic))
	mux.HandleFunc("POST /api/admin/subscriptions/{id}/status", s.admin(s.adminSubStatus))
	mux.HandleFunc("DELETE /api/admin/subscriptions/{id}", s.admin(s.adminDeleteSub))
}

func (s *Server) adminGrant(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		PlanID int64  `json:"plan_id"`
		Days   int    `json:"days"`
		Kind   string `json:"kind"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sub, err := s.svc.AdminGrant(r.Context(), customer(r), id, in.PlanID, in.Days, in.Kind)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) adminDays(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Days int `json:"days"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sub, err := s.svc.AdminExtend(r.Context(), customer(r), id, in.Days)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) adminResetTraffic(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = s.svc.AdminResetTraffic(r.Context(), customer(r), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) adminSubStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.AdminSetStatus(r.Context(), customer(r), id, in.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) adminDeleteSub(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = s.svc.AdminDeleteSubscription(r.Context(), customer(r), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) adminSettingsList(w http.ResponseWriter, r *http.Request) {
	out := map[string]json.RawMessage{}
	for _, k := range settings.Sections() {
		v, err := s.svc.Settings.Public(k)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminSettingsGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.Settings.Public(r.PathValue("section"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "раздел не найден"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(v)
}

func (s *Server) adminSettingsPut(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := decode(r, &raw); err != nil {
		s.fail(w, r, err)
		return
	}
	key := r.PathValue("section")
	if err := s.svc.Settings.Update(r.Context(), key, raw); err != nil {
		if errors.Is(err, settings.ErrUnknownSection) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "раздел не найден"})
			return
		}
		var ue *service.UserError
		if !errors.As(err, &ue) {
			err = &service.UserError{Msg: err.Error()}
		}
		s.fail(w, r, err)
		return
	}
	if key == "install" {
		s.svc.ResetGuideCache()
	}
	_ = store.AddEvent(r.Context(), s.svc.Store.Pool, customer(r).ID, "settings_changed", map[string]string{"section": key}, nil)
	s.adminSettingsGet(w, r)
}

func (s *Server) adminSettingsTest(w http.ResponseWriter, r *http.Request) {
	msg, err := s.svc.TestIntegration(r.Context(), r.PathValue("section"), customer(r))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) adminPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := store.ListPlans(r.Context(), s.svc.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Server) adminPlanSave(w http.ResponseWriter, r *http.Request) {
	var p store.Plan
	if err := decode(r, &p); err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r, "id")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p.ID = id
	} else {
		p.ID = 0
	}
	if err := service.ValidatePlan(&p); err != nil {
		s.fail(w, r, err)
		return
	}
	err := s.svc.Store.Tx(r.Context(), func(tx pgx.Tx) error { return store.SavePlan(r.Context(), tx, &p) })
	if err != nil {
		s.fail(w, r, err)
		return
	}
	saved, err := store.GetPlan(r.Context(), s.svc.Store.Pool, p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) adminPlanDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	archived, err := store.DeletePlan(r.Context(), s.svc.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"archived": archived})
}

func (s *Server) adminPlanDefault(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = store.SetDefaultPlan(r.Context(), s.svc.Store.Pool, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) adminPlanReorder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := store.ReorderPlans(r.Context(), s.svc.Store.Pool, in.IDs); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) adminSquads(w http.ResponseWriter, r *http.Request) {
	in, err := s.svc.Panel.InternalSquads(r.Context())
	if err != nil {
		s.fail(w, r, &service.UserError{Msg: "панель Remnawave недоступна: " + err.Error()})
		return
	}
	ex, err := s.svc.Panel.ExternalSquads(r.Context())
	if err != nil {
		s.fail(w, r, &service.UserError{Msg: "панель Remnawave недоступна: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"internal": in, "external": ex})
}

func qInt(r *http.Request, k string) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(k))
	return v
}

func (s *Server) adminCustomers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	plan, _ := strconv.ParseInt(q.Get("plan_id"), 10, 64)
	rows, total, err := store.ListCustomers(r.Context(), s.svc.Store.Pool, store.CustomerFilter{
		Query: q.Get("q"), Status: q.Get("status"), PlanID: plan, Tag: q.Get("tag"), Sort: q.Get("sort"),
		Limit: qInt(r, "limit"), Offset: qInt(r, "offset"),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "total": total})
}

func (s *Server) adminCustomer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := store.GetCustomer(r.Context(), s.svc.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	subs, err := s.svc.Subscriptions(r.Context(), c, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ids, _ := store.ListIdentities(r.Context(), s.svc.Store.Pool, id)
	pays, _, _ := store.ListPayments(r.Context(), s.svc.Store.Pool, store.PaymentFilter{CustomerID: id, Limit: 100})
	events, _ := s.svc.CustomerEvents(r.Context(), id, 100)
	var referrer *store.Customer
	if c.ReferrerID != nil {
		referrer, _ = store.GetCustomer(r.Context(), s.svc.Store.Pool, *c.ReferrerID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"customer": c, "subscriptions": subs, "identities": ids, "payments": pays,
		"events": events, "referrer": referrer})
}

func (s *Server) adminCustomerPatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var p store.CustomerAdminPatch
	if err := decode(r, &p); err != nil {
		s.fail(w, r, err)
		return
	}
	if p.PersonalDiscount != nil && (*p.PersonalDiscount < 0 || *p.PersonalDiscount > 100) {
		bad(w, "скидка должна быть от 0 до 100%")
		return
	}
	if err := store.PatchCustomer(r.Context(), s.svc.Store.Pool, id, p); err != nil {
		s.fail(w, r, err)
		return
	}
	admin := customer(r).ID
	_ = store.AddEvent(r.Context(), s.svc.Store.Pool, id, "admin_update", p, &admin)
	s.adminCustomer(w, r)
}

func (s *Server) adminPayments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.PaymentFilter{Status: q.Get("status"), Query: q.Get("q"), Limit: qInt(r, "limit"), Offset: qInt(r, "offset")}
	f.PlanID, _ = strconv.ParseInt(q.Get("plan_id"), 10, 64)
	if t, err := time.Parse("2006-01-02", q.Get("from")); err == nil {
		f.From = &t
	}
	if t, err := time.Parse("2006-01-02", q.Get("to")); err == nil {
		t = t.AddDate(0, 0, 1)
		f.To = &t
	}
	rows, total, err := store.ListPayments(r.Context(), s.svc.Store.Pool, f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "total": total})
}
