package api

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"bshop/internal/service"
	"bshop/internal/settings"
	"bshop/internal/store"
	"bshop/internal/yookassa"

	"github.com/google/uuid"
)

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/config", s.publicConfig)

	// auth
	mux.HandleFunc("POST /api/auth/telegram/webapp", s.limit("login", 30, time.Minute, s.loginWebApp))
	mux.HandleFunc("POST /api/auth/telegram/widget", s.limit("login", 30, time.Minute, s.loginWidget))
	mux.HandleFunc("POST /api/auth/email/send", s.limit("otp", 5, 10*time.Minute, s.emailSend))
	mux.HandleFunc("POST /api/auth/email/verify", s.limit("otpv", 20, 10*time.Minute, s.emailVerify))
	mux.HandleFunc("POST /api/auth/oauth/{provider}/start", s.limit("oauth", 30, time.Minute, s.oauthStart))
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", s.oauthCallback)
	mux.HandleFunc("POST /api/auth/logout", s.logout)

	// webhooks
	mux.HandleFunc("POST /api/webhooks/yookassa", s.yookassaWebhook)

	// cabinet
	mux.HandleFunc("GET /api/me", s.authed(s.me))
	mux.HandleFunc("DELETE /api/me/identities/{id}", s.authed(s.unlinkIdentity))
	mux.HandleFunc("GET /api/plans", s.authed(s.plans))
	mux.HandleFunc("GET /api/subscriptions", s.authed(s.subscriptions))
	mux.HandleFunc("POST /api/subscriptions/{id}/primary", s.authed(s.setPrimary))
	mux.HandleFunc("GET /api/subscriptions/{id}/devices", s.authed(s.devices))
	mux.HandleFunc("DELETE /api/subscriptions/{id}/devices", s.authed(s.deleteDevice))
	mux.HandleFunc("PATCH /api/subscriptions/{id}/devices", s.authed(s.renameDevice))
	mux.HandleFunc("POST /api/subscriptions/{id}/revoke", s.authed(s.revoke))
	mux.HandleFunc("POST /api/trial", s.authed(s.trial))
	mux.HandleFunc("GET /api/install-guide", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.svc.Guide(r.Context()))
	}))
	mux.HandleFunc("POST /api/payments", s.authed(s.limit("buy", 20, time.Minute, s.buy)))
	mux.HandleFunc("GET /api/payments", s.authed(s.myPayments))
	mux.HandleFunc("GET /api/payments/{id}", s.authed(s.paymentStatus))
	mux.HandleFunc("GET /api/payment-methods", s.authed(s.paymentMethods))
	mux.HandleFunc("DELETE /api/payment-methods/{id}", s.authed(s.deletePaymentMethod))

	s.adminRoutes(mux)
	s.fileRoutes(mux)

	// frontend
	mux.HandleFunc("/", s.spa)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Store.Pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
}

// Version is set by main.
var Version = "dev"

func (s *Server) publicConfig(w http.ResponseWriter, r *http.Request) {
	st := s.svc.Settings.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"shop_name":    st.General.ShopName,
		"bot_username": BotUsername,
		"auth": map[string]bool{
			"telegram": st.Auth.TelegramEnabled, "email": st.Auth.EmailEnabled,
			"yandex": st.Auth.YandexEnabled, "vk": st.Auth.VKEnabled,
		},
		"trial":           map[string]any{"enabled": st.Trial.Enabled, "days": st.Trial.Days},
		"maintenance":     st.General.MaintenanceMode,
		"payment_methods": paymentMethods(st.YooKassa),
		"save_card":       st.YooKassa.SaveCard && st.Payments.ShowSaveCard,
		"branding":        s.publicBranding(r.Context(), st.Branding),
		"links":           enabledLinks(st.Links),
		"install":         st.Install,
	})
}

// BotUsername is filled by the bot package at startup (for login widget and deep links).
var BotUsername = ""

var methodTitles = map[string]string{"sbp": "СБП", "bank_card": "Банковская карта", "sberbank": "SberPay",
	"tinkoff_bank": "T-Pay", "yoo_money": "ЮMoney"}

func paymentMethods(y settings.YooKassa) []map[string]string {
	out := []map[string]string{}
	if !y.Enabled {
		return out
	}
	for _, m := range y.Methods {
		t := methodTitles[m]
		if v := y.MethodTitles[m]; v != "" {
			t = v
		}
		out = append(out, map[string]string{"code": m, "title": t})
	}
	return out
}

func meta(r *http.Request) service.LoginMeta {
	m := service.LoginMeta{UserAgent: r.UserAgent(), IP: clientIP(r)}
	if c := customer(r); c != nil {
		m.LinkTo = &c.ID
	}
	return m
}

func (s *Server) loginWebApp(w http.ResponseWriter, r *http.Request) {
	r = s.optionalAuth(r)
	var in struct {
		InitData string `json:"init_data"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.svc.LoginTelegramWebApp(r.Context(), in.InitData, meta(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) loginWidget(w http.ResponseWriter, r *http.Request) {
	r = s.optionalAuth(r)
	var in map[string]any
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	fields := map[string]string{}
	for k, v := range in {
		switch t := v.(type) {
		case string:
			fields[k] = t
		case float64:
			fields[k] = strconv.FormatInt(int64(t), 10)
		case json.Number:
			fields[k] = t.String()
		}
	}
	sess, err := s.svc.LoginTelegramWidget(r.Context(), fields, meta(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) emailSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.SendEmailCode(r.Context(), in.Email); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (s *Server) emailVerify(w http.ResponseWriter, r *http.Request) {
	r = s.optionalAuth(r)
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.svc.VerifyEmailCode(r.Context(), in.Email, in.Code, meta(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	r = s.optionalAuth(r)
	var in struct {
		ReturnTo string `json:"return_to"`
	}
	_ = decode(r, &in)
	var linkTo *int64
	if c := customer(r); c != nil {
		linkTo = &c.ID
	}
	u, err := s.svc.OAuthStart(r.Context(), r.PathValue("provider"), linkTo, in.ReturnTo)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// oauthCallback lands on the frontend with the session token in the URL fragment (never sent to servers or logs).
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sess, ret, err := s.svc.OAuthCallback(r.Context(), r.PathValue("provider"), q.Get("code"), q.Get("state"), q.Get("device_id"), meta(r))
	frag := url.Values{}
	if err != nil {
		var ue *service.UserError
		msg := "не удалось войти"
		if errorsAs(err, &ue) {
			msg = ue.Msg
		} else {
			s.log.Error("oauth callback", "err", err)
		}
		frag.Set("error", msg)
	} else if sess.Token != "" {
		frag.Set("token", sess.Token)
	} else {
		frag.Set("linked", r.PathValue("provider"))
	}
	frag.Set("return", ret)
	http.Redirect(w, r, "/auth/done#"+frag.Encode(), http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if tok := bearer(r); tok != "" {
		_ = s.svc.Logout(r.Context(), tok)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) yookassaWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var n yookassa.Notification
	if err := json.Unmarshal(body, &n); err != nil || n.Object.ID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// The object is re-read from the YooKassa API, so a forged notification cannot mark anything paid.
	if err := s.svc.HandleYooKassaNotification(r.Context(), n); err != nil {
		s.log.Error("yookassa webhook", "event", n.Event, "id", n.Object.ID, "err", err)
		w.WriteHeader(http.StatusInternalServerError) // YooKassa retries
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- cabinet ----

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	c := customer(r)
	ids, err := store.ListIdentities(r.Context(), s.svc.Store.Pool, c.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st := s.svc.Settings.Get()
	n, _ := store.CountSubscriptions(r.Context(), s.svc.Store.Pool, c.ID, nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"customer":        c,
		"identities":      ids,
		"trial_available": st.Trial.Enabled && !c.TrialUsed && n == 0,
		"ref_link":        refLink(c),
	})
}

func refLink(c *store.Customer) string {
	if c.RefCode == nil || BotUsername == "" {
		return ""
	}
	return "https://t.me/" + BotUsername + "?start=ref_" + *c.RefCode
}

func (s *Server) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = s.svc.UnlinkIdentity(r.Context(), customer(r).ID, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) plans(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Catalog(r.Context(), customer(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) subscriptions(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Subscriptions(r.Context(), customer(r), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setPrimary(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = s.svc.SetPrimary(r.Context(), customer(r), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.svc.Devices(r.Context(), customer(r), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = s.svc.DeleteDevice(r.Context(), customer(r), id, r.URL.Query().Get("hwid"))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) renameDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		HWID string `json:"hwid"`
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.RenameDevice(r.Context(), customer(r), id, in.HWID, in.Name); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		OnlyPasswords bool `json:"only_passwords"`
	}
	_ = decode(r, &in)
	sub, err := s.svc.RevokeLink(r.Context(), customer(r), id, in.OnlyPasswords)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) trial(w http.ResponseWriter, r *http.Request) {
	sub, err := s.svc.ActivateTrial(r.Context(), customer(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) buy(w http.ResponseWriter, r *http.Request) {
	var in service.BuyRequest
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.svc.Buy(r.Context(), customer(r), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) myPayments(w http.ResponseWriter, r *http.Request) {
	rows, total, err := store.ListPayments(r.Context(), s.svc.Store.Pool, store.PaymentFilter{
		CustomerID: customer(r).ID, Status: r.URL.Query().Get("status"), Limit: 100})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range rows {
		rows[i].ConfirmationURL = "" // only exposed on creation
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "total": total})
}

func (s *Server) paymentStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		bad(w, "неверный платёж")
		return
	}
	p, err := s.svc.CheckPayment(r.Context(), customer(r), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID, "status": p.Status, "applied": p.AppliedAt != nil,
		"amount": p.Amount, "title": p.Title, "confirmation_url": p.ConfirmationURL})
}

func (s *Server) paymentMethods(w http.ResponseWriter, r *http.Request) {
	m, err := store.ListPaymentMethods(r.Context(), s.svc.Store.Pool, customer(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deletePaymentMethod(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = store.DeletePaymentMethod(r.Context(), s.svc.Store.Pool, customer(r).ID, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- frontend ----

func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "не найдено"})
		return
	}
	if s.web == nil {
		http.Error(w, "frontend is not built", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p != "" {
		if f, err := s.web.Open(p); err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.web, p)
				return
			}
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	idx, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		http.Error(w, "frontend is not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(idx)
}

func (s *Server) publicBranding(ctx context.Context, b settings.Branding) map[string]any {
	return map[string]any{
		"title": b.Title, "tagline": b.Tagline, "logo": orDefault(fileURL(b.LogoFile), "/logo-default.png"),
		"colors": map[string]string{"primary": b.Primary, "background": b.Background, "surface": b.Surface,
			"text": b.Text, "muted": b.Muted},
		"card_opacity": b.CardOpacity, "card_blur": b.CardBlur,
		"bg_mobile": orDefault(s.mediaURL(ctx, b.BgMobileFile), "/bg-default.webp"), "bg_desktop": s.mediaURL(ctx, b.BgDesktopFile),
		"bg_fit": b.BgFit, "bg_opacity": b.BgOpacity,
		"banner": s.mediaURL(ctx, b.BannerFile), "banner_link": b.BannerLink,
		"login_text": b.LoginText, "effect": b.Effect,
		"show_header": b.ShowHeader, "hero_space": b.HeroSpace, "bg_dim": b.BgDim,
	}
}

func enabledLinks(l settings.Links) []settings.Link {
	out := []settings.Link{}
	for _, it := range l.Items {
		if it.Enabled && it.URL != "" {
			out = append(out, it)
		}
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
