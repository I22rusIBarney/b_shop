package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"

	"github.com/google/uuid"
)

func (s *Server) stage4Routes(mux *http.ServeMux) {
	pool := func() store.DB { return s.svc.Store.Pool }

	// ---- cabinet: support ----
	mux.HandleFunc("GET /api/support/config", s.authed(s.handle(func(r *http.Request) (any, error) {
		st := s.svc.Settings.Get().Support
		faq, err := store.ListFAQ(r.Context(), pool(), true)
		return map[string]any{"enabled": st.Enabled, "faq": faq, "max_files": st.AttachMaxFiles, "max_mb": st.AttachMaxMB,
			"rating": st.RatingEnabled}, err
	})))
	mux.HandleFunc("GET /api/tickets", s.authed(s.handle(func(r *http.Request) (any, error) {
		return s.svc.CustomerTickets(r.Context(), customer(r))
	})))
	mux.HandleFunc("POST /api/tickets", s.authed(s.limit("ticket", 10, time.Hour, s.handle(withBody(func(r *http.Request, in struct {
		Subject     string      `json:"subject"`
		Body        string      `json:"body"`
		Attachments []uuid.UUID `json:"attachments"`
	}) (any, error) {
		return s.svc.CreateTicket(r.Context(), customer(r), in.Subject, in.Body, in.Attachments, "web")
	})))))
	mux.HandleFunc("GET /api/tickets/{id}", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.CustomerTicket(r.Context(), customer(r), id)
	})))
	mux.HandleFunc("POST /api/tickets/{id}/messages", s.authed(s.limit("ticketmsg", 60, time.Hour, s.handle(withBody(func(r *http.Request, in struct {
		Body        string      `json:"body"`
		Attachments []uuid.UUID `json:"attachments"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.CustomerMessage(r.Context(), customer(r), id, in.Body, in.Attachments)
	})))))
	mux.HandleFunc("POST /api/tickets/{id}/close", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, s.svc.CloseTicketByCustomer(r.Context(), customer(r), id)
	})))
	mux.HandleFunc("POST /api/tickets/{id}/rate", s.authed(s.handle(withBody(func(r *http.Request, in struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, s.svc.RateTicket(r.Context(), customer(r), id, in.Rating, in.Comment)
	}))))
	mux.HandleFunc("POST /api/files", s.authed(s.limit("upload", 30, time.Hour, s.customerUpload)))

	// ---- cabinet: news and badges ----
	mux.HandleFunc("GET /api/news", s.authed(s.handle(func(r *http.Request) (any, error) {
		return s.svc.News(r.Context(), customer(r))
	})))
	mux.HandleFunc("GET /api/badges", s.authed(s.handle(func(r *http.Request) (any, error) {
		c := customer(r)
		news, _ := store.UnreadNews(r.Context(), pool(), c)
		var tickets int
		_ = pool().QueryRow(r.Context(), `SELECT count(*) FROM tickets WHERE customer_id=$1 AND unread_customer`, c.ID).Scan(&tickets)
		out := map[string]int{"news": news, "tickets": tickets}
		if c.IsAdmin {
			out["admin_tickets"], _ = store.CountTicketsNeedingReply(r.Context(), pool())
		}
		return out, nil
	})))

	// ---- admin: tickets ----
	mux.HandleFunc("GET /api/admin/tickets", s.admin(s.handle(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		cid, _ := strconv.ParseInt(q.Get("customer"), 10, 64)
		rows, total, err := store.ListTickets(r.Context(), pool(), store.TicketFilter{Status: q.Get("status"), Search: strings.TrimSpace(q.Get("q")),
			Customer: cid, Limit: qInt(r, "limit"), Offset: qInt(r, "offset")})
		return map[string]any{"items": rows, "total": total}, err
	})))
	mux.HandleFunc("GET /api/admin/tickets/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.AdminTicket(r.Context(), id)
	})))
	mux.HandleFunc("POST /api/admin/tickets/{id}/messages", s.admin(s.handle(withBody(func(r *http.Request, in service.OperatorReply) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.Reply(r.Context(), customer(r), id, in)
	}))))
	mux.HandleFunc("PATCH /api/admin/tickets/{id}", s.admin(s.handle(withBody(func(r *http.Request, in service.TicketPatch) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.PatchTicket(r.Context(), customer(r), id, in)
	}))))
	mux.HandleFunc("POST /api/admin/tickets/{id}/suggest", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.SuggestReply(r.Context(), id)
	})))
	mux.HandleFunc("POST /api/admin/ticket-messages/{id}/draft", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		Action string `json:"action"`
		Body   string `json:"body"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, s.svc.DraftAction(r.Context(), customer(r), id, in.Action, in.Body)
	}))))
	mux.HandleFunc("POST /api/admin/tickets/files", s.admin(s.customerUpload))

	// templates, FAQ, knowledge base
	crud(mux, s, "templates",
		func(r *http.Request) (any, error) { return store.ListTemplates(r.Context(), pool()) },
		func(r *http.Request, id int64) (any, error) {
			var t store.SupportTemplate
			if err := decode(r, &t); err != nil {
				return nil, err
			}
			if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Body) == "" {
				return nil, &service.UserError{Msg: "нужны название и текст"}
			}
			t.ID = id
			return t, store.SaveTemplate(r.Context(), pool(), &t)
		},
		func(r *http.Request, id int64) error { return store.DeleteTemplate(r.Context(), pool(), id) })
	crud(mux, s, "faq",
		func(r *http.Request) (any, error) { return store.ListFAQ(r.Context(), pool(), false) },
		func(r *http.Request, id int64) (any, error) {
			var f store.FAQItem
			if err := decode(r, &f); err != nil {
				return nil, err
			}
			if strings.TrimSpace(f.Question) == "" || strings.TrimSpace(f.Answer) == "" {
				return nil, &service.UserError{Msg: "нужны вопрос и ответ"}
			}
			f.ID = id
			return f, store.SaveFAQ(r.Context(), pool(), &f)
		},
		func(r *http.Request, id int64) error { return store.DeleteFAQ(r.Context(), pool(), id) })
	crud(mux, s, "kb",
		func(r *http.Request) (any, error) { return store.ListKB(r.Context(), pool(), false) },
		func(r *http.Request, id int64) (any, error) {
			var a store.KBArticle
			if err := decode(r, &a); err != nil {
				return nil, err
			}
			if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Body) == "" {
				return nil, &service.UserError{Msg: "нужны заголовок и текст"}
			}
			a.ID = id
			return a, store.SaveKB(r.Context(), pool(), &a)
		},
		func(r *http.Request, id int64) error { return store.DeleteKB(r.Context(), pool(), id) })

	// ---- admin: notifications ----
	mux.HandleFunc("GET /api/admin/notifications/events", s.admin(s.handle(func(r *http.Request) (any, error) {
		return service.AdminEvents, nil
	})))
	mux.HandleFunc("POST /api/admin/notifications/test", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		Event string `json:"event"`
	}) (any, error) {
		return nil, s.svc.TestAdminEvent(r.Context(), in.Event, customer(r))
	}))))

	// ---- admin: broadcasts ----
	mux.HandleFunc("GET /api/admin/broadcasts", s.admin(s.handle(func(r *http.Request) (any, error) {
		list, err := store.ListBroadcasts(r.Context(), pool(), 100)
		return map[string]any{"items": list, "segments": service.Segments()}, err
	})))
	mux.HandleFunc("POST /api/admin/broadcasts/audience", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		Audience store.BroadcastAudience `json:"audience"`
		Channel  string                  `json:"channel"`
	}) (any, error) {
		n, err := s.svc.BroadcastAudienceSize(r.Context(), in.Audience, in.Channel)
		return map[string]int{"count": n}, err
	}))))
	mux.HandleFunc("POST /api/admin/broadcasts/draft", s.admin(s.handle(withBody(func(r *http.Request, in store.Broadcast) (any, error) {
		return s.svc.SaveBroadcastDraft(r.Context(), customer(r), &in)
	}))))
	mux.HandleFunc("POST /api/admin/broadcasts/test", s.admin(s.limit("bctest", 20, time.Minute, s.handle(withBody(func(r *http.Request, in store.Broadcast) (any, error) {
		return nil, s.svc.TestBroadcast(r.Context(), customer(r), &in)
	})))))
	mux.HandleFunc("POST /api/admin/broadcasts/send", s.admin(s.handle(withBody(func(r *http.Request, in store.Broadcast) (any, error) {
		return s.svc.SubmitBroadcast(r.Context(), customer(r), &in)
	}))))
	mux.HandleFunc("POST /api/admin/broadcasts/{id}/cancel", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.CancelBroadcast(r.Context(), pool(), id)
	})))
	mux.HandleFunc("POST /api/admin/broadcasts/{id}/news", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		On bool `json:"on"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.SetBroadcastNews(r.Context(), pool(), id, in.On)
	}))))
	mux.HandleFunc("DELETE /api/admin/broadcasts/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		b, err := store.GetBroadcast(r.Context(), pool(), id)
		if err != nil {
			return nil, err
		}
		if b.Status == "sending" {
			return nil, &service.UserError{Msg: "сначала остановите рассылку"}
		}
		return nil, store.DeleteBroadcast(r.Context(), pool(), id)
	})))

	// ---- admin: automations ----
	mux.HandleFunc("GET /api/admin/automations", s.admin(s.handle(func(r *http.Request) (any, error) {
		items, warnings, err := s.svc.AutomationsOverview(r.Context())
		if warnings == nil {
			warnings = []string{}
		}
		return map[string]any{"items": items, "warnings": warnings, "triggers": service.Triggers,
			"enabled": s.svc.Settings.Get().Automations.Enabled}, err
	})))
	saveAuto := func(r *http.Request, id int64) (any, error) {
		var a store.Automation
		if err := decode(r, &a); err != nil {
			return nil, err
		}
		a.ID = id
		if err := service.ValidateAutomation(&a); err != nil {
			return nil, err
		}
		return a, store.SaveAutomation(r.Context(), pool(), &a)
	}
	mux.HandleFunc("POST /api/admin/automations", s.admin(s.handle(func(r *http.Request) (any, error) { return saveAuto(r, 0) })))
	mux.HandleFunc("PUT /api/admin/automations/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return saveAuto(r, id)
	})))
	mux.HandleFunc("POST /api/admin/automations/{id}/active", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		On bool `json:"on"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.SetAutomationActive(r.Context(), pool(), id, in.On)
	}))))
	mux.HandleFunc("DELETE /api/admin/automations/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.DeleteAutomation(r.Context(), pool(), id)
	})))
	mux.HandleFunc("GET /api/admin/automations/export", s.admin(func(w http.ResponseWriter, r *http.Request) {
		out, err := s.svc.ExportAutomations(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="automations-`+time.Now().Format("2006-01-02")+`.json"`)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	}))
	mux.HandleFunc("POST /api/admin/automations/import", s.admin(s.handle(func(r *http.Request) (any, error) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			return nil, err
		}
		return s.svc.ImportAutomations(r.Context(), raw)
	})))
	mux.HandleFunc("POST /api/admin/automations/test-step", s.admin(s.limit("autotest", 20, time.Minute, s.handle(withBody(func(r *http.Request, in struct {
		Step     json.RawMessage `json:"step"`
		PlanName string          `json:"plan_name"`
	}) (any, error) {
		return nil, s.svc.TestAutomationStep(r.Context(), customer(r), in.Step, map[string]any{"plan_name": in.PlanName})
	})))))
	mux.HandleFunc("GET /api/admin/customers/{id}/automations", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return store.CustomerRuns(r.Context(), pool(), id)
	})))
	mux.HandleFunc("POST /api/admin/customers/{id}/automations/stop", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		RunID int64 `json:"run_id"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.StopCustomerRuns(r.Context(), pool(), id, in.RunID)
	}))))
}

// crud registers GET/POST/PUT/DELETE /api/admin/support/{name}.
func crud(mux *http.ServeMux, s *Server, name string, list func(*http.Request) (any, error),
	save func(*http.Request, int64) (any, error), del func(*http.Request, int64) error) {
	base := "/api/admin/support/" + name
	mux.HandleFunc("GET "+base, s.admin(s.handle(list)))
	mux.HandleFunc("POST "+base, s.admin(s.handle(func(r *http.Request) (any, error) { return save(r, 0) })))
	mux.HandleFunc("PUT "+base+"/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return save(r, id)
	})))
	mux.HandleFunc("DELETE "+base+"/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, del(r, id)
	})))
}

// customerUpload stores a ticket attachment owned by the uploader.
func (s *Server) customerUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		bad(w, "файл слишком большой")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		bad(w, "выберите файл")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 21<<20))
	if err != nil {
		bad(w, "не удалось прочитать файл")
		return
	}
	mime := http.DetectContentType(data)
	if i := strings.Index(mime, ";"); i > 0 {
		mime = mime[:i]
	}
	file, err := s.svc.SaveCustomerFile(r.Context(), customer(r), hdr.Filename, mime, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": file.ID, "name": file.Name, "mime": file.Mime, "size": file.Size, "url": "/files/" + file.ID.String()})
}
