package api

import (
	"errors"
	"net/http"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"

	"github.com/jackc/pgx/v5/pgconn"
)

// handle wraps a handler that returns a JSON value or an error.
func (s *Server) handle(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := fn(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if out == nil {
			out = map[string]bool{"ok": true}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// withBody decodes a JSON body into T before calling fn.
func withBody[T any](fn func(r *http.Request, in T) (any, error)) func(r *http.Request) (any, error) {
	return func(r *http.Request) (any, error) {
		var in T
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return fn(r, in)
	}
}

func (s *Server) stage3Routes(mux *http.ServeMux) {
	// promo codes (cabinet)
	mux.HandleFunc("POST /api/promo", s.authed(s.limit("promo", 10, time.Minute, s.handle(withBody(func(r *http.Request, in struct {
		Code           string `json:"code"`
		SubscriptionID int64  `json:"subscription_id"`
	}) (any, error) {
		return s.svc.RedeemPromo(r.Context(), customer(r), in.Code, in.SubscriptionID)
	})))))
	mux.HandleFunc("GET /api/promo", s.authed(s.handle(func(r *http.Request) (any, error) {
		a, err := s.svc.ActivePromos(r.Context(), customer(r))
		return map[string]any{"active": a}, err
	})))
	mux.HandleFunc("DELETE /api/promo/{id}", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.DeleteRedemption(r.Context(), s.svc.Store.Pool, id, customer(r).ID)
	})))

	// plan change, extra devices, autopay
	mux.HandleFunc("GET /api/subscriptions/{id}/change", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.ChangeOptions(r.Context(), customer(r), id)
	})))
	mux.HandleFunc("POST /api/subscriptions/{id}/change", s.authed(s.limit("buy", 20, time.Minute, s.handle(withBody(func(r *http.Request, in service.ChangeRequest) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.ChangePlan(r.Context(), customer(r), id, in)
	})))))
	mux.HandleFunc("GET /api/subscriptions/{id}/extra-devices", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.DevicesOffer(r.Context(), customer(r), id)
	})))
	mux.HandleFunc("POST /api/subscriptions/{id}/extra-devices", s.authed(s.limit("buy", 20, time.Minute, s.handle(withBody(func(r *http.Request, in service.DevicesRequest) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.BuyDevices(r.Context(), customer(r), id, in)
	})))))
	mux.HandleFunc("GET /api/subscriptions/{id}/traffic", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.TrafficOptions(r.Context(), customer(r), id)
	})))
	mux.HandleFunc("POST /api/subscriptions/{id}/traffic", s.authed(s.limit("buy", 20, time.Minute, s.handle(withBody(func(r *http.Request, in trafficRequest) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.BuyTraffic(r.Context(), customer(r), id, in.Package, in.Method, in.ReturnURL)
	})))))
	mux.HandleFunc("POST /api/subscriptions/{id}/traffic/reset", s.authed(s.limit("buy", 20, time.Minute, s.handle(withBody(func(r *http.Request, in trafficRequest) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.ResetTrafficPaid(r.Context(), customer(r), id, in.Method, in.ReturnURL)
	})))))
	mux.HandleFunc("GET /api/banners", s.authed(s.handle(func(r *http.Request) (any, error) {
		return s.svc.BannersFor(r.Context(), customer(r))
	})))
	mux.HandleFunc("POST /api/banners/{id}/dismiss", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, s.svc.DismissBanner(r.Context(), customer(r), id)
	})))
	mux.HandleFunc("GET /api/admin/banners", s.admin(s.handle(func(r *http.Request) (any, error) {
		return store.ListBanners(r.Context(), s.svc.Store.Pool)
	})))
	saveBanner := func(r *http.Request, id int64) (any, error) {
		var b store.Banner
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		b.ID = id
		if id != 0 {
			old, err := store.GetBanner(r.Context(), s.svc.Store.Pool, id)
			if err != nil {
				return nil, err
			}
			b.Kind = old.Kind // system banners keep their logic
		} else {
			b.Kind = ""
		}
		if err := service.ValidateBanner(&b); err != nil {
			return nil, err
		}
		return b, store.SaveBanner(r.Context(), s.svc.Store.Pool, &b)
	}
	mux.HandleFunc("POST /api/admin/banners", s.admin(s.handle(func(r *http.Request) (any, error) { return saveBanner(r, 0) })))
	mux.HandleFunc("PUT /api/admin/banners/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return saveBanner(r, id)
	})))
	mux.HandleFunc("DELETE /api/admin/banners/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.DeleteBanner(r.Context(), s.svc.Store.Pool, id)
	})))
	mux.HandleFunc("POST /api/admin/banners/reorder", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		IDs []int64 `json:"ids"`
	}) (any, error) {
		return nil, store.ReorderBanners(r.Context(), s.svc.Store.Pool, in.IDs)
	}))))
	mux.HandleFunc("PUT /api/subscriptions/{id}/traffic/auto", s.authed(s.handle(withBody(func(r *http.Request, in struct {
		On      bool `json:"on"`
		Package int  `json:"package"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.SetAutoTopup(r.Context(), customer(r), id, in.On, in.Package)
	}))))
	mux.HandleFunc("GET /api/locations", s.authed(s.handle(func(r *http.Request) (any, error) {
		return s.svc.Locations(r.Context())
	})))
	mux.HandleFunc("GET /api/subscriptions/{id}/autopay", s.authed(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.Autopay(r.Context(), customer(r), id)
	})))
	mux.HandleFunc("PUT /api/subscriptions/{id}/autopay", s.authed(s.handle(withBody(func(r *http.Request, in struct {
		Enabled  bool  `json:"enabled"`
		MethodID int64 `json:"method_id"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.SetAutopay(r.Context(), customer(r), id, in.Enabled, in.MethodID)
	}))))
	mux.HandleFunc("GET /api/referral", s.authed(s.handle(func(r *http.Request) (any, error) {
		return s.svc.Referral(r.Context(), customer(r), refLink(customer(r)))
	})))

	// admin: promo codes
	mux.HandleFunc("GET /api/admin/promos", s.admin(s.handle(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		f := store.PromoFilter{Search: q.Get("q"), Kind: q.Get("kind")}
		if q.Has("folder") {
			v := q.Get("folder")
			f.Folder = &v
		}
		list, err := store.ListPromos(r.Context(), s.svc.Store.Pool, f)
		if err != nil {
			return nil, err
		}
		folders, err := store.PromoFolders(r.Context(), s.svc.Store.Pool)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		type row struct {
			store.Promo
			State string `json:"state"`
		}
		out := make([]row, 0, len(list))
		for _, p := range list {
			out = append(out, row{Promo: p, State: p.State(now)})
		}
		return map[string]any{"items": out, "folders": folders}, nil
	})))
	savePromo := func(r *http.Request, in store.Promo) (any, error) {
		if r.PathValue("id") != "" {
			id, err := pathID(r, "id")
			if err != nil {
				return nil, err
			}
			in.ID = id
		} else {
			in.ID = 0
		}
		if in.Code == "" {
			in.Code = service.GeneratePromoCode("")
		}
		if err := service.ValidatePromo(&in); err != nil {
			return nil, err
		}
		err := store.SavePromo(r.Context(), s.svc.Store.Pool, &in)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, &service.UserError{Msg: "такой код уже есть"}
		}
		return in, err
	}
	mux.HandleFunc("POST /api/admin/promos", s.admin(s.handle(withBody(savePromo))))
	mux.HandleFunc("PUT /api/admin/promos/{id}", s.admin(s.handle(withBody(savePromo))))
	mux.HandleFunc("DELETE /api/admin/promos/{id}", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, store.DeletePromo(r.Context(), s.svc.Store.Pool, id)
	})))
	mux.HandleFunc("GET /api/admin/promos/{id}/redemptions", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return store.ListRedemptions(r.Context(), s.svc.Store.Pool, id, 0)
	})))
	mux.HandleFunc("GET /api/admin/promos/generate", s.admin(s.handle(func(r *http.Request) (any, error) {
		return map[string]string{"code": service.GeneratePromoCode(r.URL.Query().Get("prefix"))}, nil
	})))
	// Apply a code on behalf of a customer.
	mux.HandleFunc("POST /api/admin/customers/{id}/promo", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		Code string `json:"code"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		c, err := store.GetCustomer(r.Context(), s.svc.Store.Pool, id)
		if err != nil {
			return nil, err
		}
		return s.svc.RedeemPromo(r.Context(), c, in.Code, 0)
	}))))
	mux.HandleFunc("POST /api/admin/customers/{id}/bonus", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		Days int `json:"days"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, s.svc.AdminReferralBonus(r.Context(), customer(r), id, in.Days)
	}))))
	mux.HandleFunc("POST /api/admin/subscriptions/{id}/autopay", s.admin(s.handle(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, s.svc.ChargeAutopayNow(r.Context(), id)
	})))
	mux.HandleFunc("POST /api/admin/subscriptions/{id}/change", s.admin(s.handle(withBody(func(r *http.Request, in struct {
		PlanID int64 `json:"plan_id"`
		Days   *int  `json:"days"`
	}) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.svc.AdminChangePlan(r.Context(), customer(r), id, in.PlanID, in.Days)
	}))))
}

type trafficRequest struct {
	Package   int    `json:"package"`
	Method    string `json:"method"`
	ReturnURL string `json:"return_url"`
}
