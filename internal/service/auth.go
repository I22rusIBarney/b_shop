package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"bshop/internal/auth"
	"bshop/internal/store"

	"github.com/jackc/pgx/v5"
)

// LoginMeta describes where a login came from.
type LoginMeta struct {
	UserAgent string
	IP        string
	// LinkTo attaches the login to an already signed-in customer instead of signing in.
	LinkTo *int64
}

type Session struct {
	Token    string          `json:"token"`
	Customer *store.Customer `json:"customer"`
	Created  bool            `json:"created"`
}

var ErrIdentityTaken = &UserError{Msg: "этот способ входа уже привязан к другому аккаунту"}

// Login finds or creates the customer for a verified provider profile and opens a session.
func (s *Service) Login(ctx context.Context, p *auth.Profile, meta LoginMeta) (*Session, error) {
	var cust *store.Customer
	created := false
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		cust, created, err = s.resolveIdentity(ctx, tx, p, meta.LinkTo)
		return err
	})
	if err != nil {
		return nil, err
	}
	if cust.IsBlocked {
		return nil, userErr("доступ к аккаунту ограничен, обратитесь в поддержку")
	}
	if meta.LinkTo != nil {
		_ = store.AddEvent(ctx, s.Store.Pool, cust.ID, "login_linked", map[string]string{"provider": p.Provider}, nil)
		return &Session{Customer: cust}, nil
	}
	token := auth.RandomToken(32)
	ttl := time.Duration(s.set().Auth.SessionDays) * 24 * time.Hour
	if err := store.CreateSession(ctx, s.Store.Pool, token, cust.ID, p.Provider, meta.UserAgent, meta.IP, ttl); err != nil {
		return nil, err
	}
	if created {
		_ = store.AddEvent(ctx, s.Store.Pool, cust.ID, "registered", map[string]string{"provider": p.Provider}, nil)
		s.event(ctx, "new_customer", cust, map[string]any{"via": " (" + p.Provider + ")"})
	}
	_ = store.AddEvent(ctx, s.Store.Pool, cust.ID, "login", map[string]string{"provider": p.Provider}, nil)
	return &Session{Token: token, Customer: cust, Created: created}, nil
}

func strp(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func (s *Service) resolveIdentity(ctx context.Context, tx pgx.Tx, p *auth.Profile, linkTo *int64) (*store.Customer, bool, error) {
	ident, err := store.FindIdentity(ctx, tx, p.Provider, p.Subject)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	var tgID *int64
	if p.Provider == "telegram" {
		id, _ := strconv.ParseInt(p.Subject, 10, 64)
		tgID = &id
	}

	if ident != nil {
		if linkTo != nil && ident.CustomerID != *linkTo {
			return nil, false, ErrIdentityTaken
		}
		_ = store.TouchIdentity(ctx, tx, ident.ID, p.Display)
		if tgID != nil {
			_ = store.UpdateTelegramProfile(ctx, tx, ident.CustomerID, strp(p.Username), strp(p.FirstName), strp(p.LastName), p.Language)
		}
		c, err := store.GetCustomer(ctx, tx, ident.CustomerID)
		return c, false, err
	}

	// Attach to the signed-in customer.
	if linkTo != nil {
		c, err := store.GetCustomerForUpdate(ctx, tx, *linkTo)
		if err != nil {
			return nil, false, err
		}
		if tgID != nil {
			if other, err := store.GetCustomerByTelegram(ctx, tx, *tgID); err == nil && other.ID != c.ID {
				return nil, false, ErrIdentityTaken
			}
			if c.TelegramID == nil {
				if err := store.SetCustomerTelegram(ctx, tx, c.ID, *tgID); err != nil {
					return nil, false, err
				}
			}
		}
		if p.Email != "" && c.Email == nil {
			if other, err := store.GetCustomerByEmail(ctx, tx, p.Email); err == nil && other.ID != c.ID {
				return nil, false, ErrIdentityTaken
			}
			if err := store.SetCustomerEmail(ctx, tx, c.ID, p.Email); err != nil {
				return nil, false, err
			}
		}
		if err := store.AddIdentity(ctx, tx, c.ID, p.Provider, p.Subject, p.Display); err != nil {
			return nil, false, err
		}
		c, err = store.GetCustomer(ctx, tx, c.ID)
		return c, false, err
	}

	// Existing customer known by Telegram id or verified email (imported data, other login method).
	var existing *store.Customer
	if tgID != nil {
		existing, err = store.GetCustomerByTelegram(ctx, tx, *tgID)
	} else if p.Email != "" {
		existing, err = store.GetCustomerByEmail(ctx, tx, p.Email)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	if existing != nil {
		if err := store.AddIdentity(ctx, tx, existing.ID, p.Provider, p.Subject, p.Display); err != nil {
			return nil, false, err
		}
		if tgID != nil {
			_ = store.UpdateTelegramProfile(ctx, tx, existing.ID, strp(p.Username), strp(p.FirstName), strp(p.LastName), p.Language)
		}
		return existing, false, nil
	}

	nc := store.NewCustomer{TelegramID: tgID, Username: strp(p.Username), FirstName: strp(p.FirstName),
		LastName: strp(p.LastName), Language: normLang(p.Language), Source: p.Provider}
	if p.Email != "" {
		nc.Email = &p.Email
	}
	if tgID != nil && s.Cfg.IsAdminTelegram(*tgID) {
		nc.IsAdmin = true
	}
	if ref := referralCode(p.StartParam); ref != "" {
		if r, err := store.GetCustomerByRefCode(ctx, tx, ref); err == nil {
			nc.ReferrerID = &r.ID
			nc.Source = "referral"
		}
	}
	c, err := store.CreateCustomer(ctx, tx, nc)
	if err != nil {
		return nil, false, err
	}
	if err := store.AddIdentity(ctx, tx, c.ID, p.Provider, p.Subject, p.Display); err != nil {
		return nil, false, err
	}
	return c, true, nil
}

func normLang(l string) string {
	if strings.HasPrefix(l, "en") {
		return "en"
	}
	return "ru"
}

// referralCode extracts a referral code from a Telegram start parameter ("ref_ABC123" or "r_ABC123").
func referralCode(start string) string {
	for _, p := range []string{"ref_", "r_", "ref"} {
		if strings.HasPrefix(start, p) {
			return strings.ToUpper(strings.TrimPrefix(start, p))
		}
	}
	return ""
}

// AuthenticateToken resolves a session token to its customer.
func (s *Service) AuthenticateToken(ctx context.Context, token string) (*store.Customer, error) {
	c, err := store.SessionCustomer(ctx, s.Store.Pool, token)
	if err != nil {
		return nil, err
	}
	_ = store.TouchCustomer(ctx, s.Store.Pool, c.ID)
	return c, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return store.DeleteSession(ctx, s.Store.Pool, token)
}

// ---- Telegram ----

func (s *Service) LoginTelegramWebApp(ctx context.Context, initData string, meta LoginMeta) (*Session, error) {
	if !s.set().Auth.TelegramEnabled && meta.LinkTo == nil {
		return nil, userErr("вход через Telegram отключён")
	}
	if strings.TrimSpace(initData) == "" {
		return nil, userErr("Telegram не передал данные запуска")
	}
	p, err := auth.VerifyWebAppInitData(initData, s.Cfg.BotToken, 30*24*time.Hour)
	if err != nil {
		s.Log.Warn("telegram mini app login rejected", "reason", err)
		switch {
		case errors.Is(err, auth.ErrSignature):
			return nil, userErr("подпись Telegram не совпала: кабинет открыт из другого бота, чем указан в BOT_TOKEN")
		case errors.Is(err, auth.ErrClock):
			return nil, userErr("часы сервера отстают — проверьте время на сервере (timedatectl)")
		case errors.Is(err, auth.ErrExpired):
			return nil, userErr("данные запуска устарели, откройте приложение заново")
		}
		return nil, userErr("не удалось проверить данные Telegram, откройте приложение заново")
	}
	return s.Login(ctx, p, meta)
}

func (s *Service) LoginTelegramWidget(ctx context.Context, fields map[string]string, meta LoginMeta) (*Session, error) {
	if !s.set().Auth.TelegramEnabled && meta.LinkTo == nil {
		return nil, userErr("вход через Telegram отключён")
	}
	p, err := auth.VerifyLoginWidget(fields, s.Cfg.BotToken, 24*time.Hour)
	if err != nil {
		return nil, userErr("не удалось проверить вход через Telegram")
	}
	return s.Login(ctx, p, meta)
}

// TelegramBotLogin creates/updates a customer from a bot update (no session).
func (s *Service) TelegramBotUser(ctx context.Context, p *auth.Profile) (*store.Customer, bool, error) {
	var c *store.Customer
	var created bool
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		c, created, err = s.resolveIdentity(ctx, tx, p, nil)
		return err
	})
	if err == nil && created {
		_ = store.AddEvent(ctx, s.Store.Pool, c.ID, "registered", map[string]string{"provider": "telegram_bot"}, nil)
		s.event(ctx, "new_customer", c, map[string]any{"via": ""})
	}
	return c, created, err
}

// ---- Email one-time codes ----

func NormalizeEmail(e string) (string, error) {
	e = strings.ToLower(strings.TrimSpace(e))
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || !strings.Contains(e[strings.LastIndex(e, "@"):], ".") {
		return "", userErr("неверный адрес почты")
	}
	return e, nil
}

func (s *Service) SendEmailCode(ctx context.Context, rawEmail string) error {
	if !s.set().Auth.EmailEnabled {
		return userErr("вход по почте отключён")
	}
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	if prev, err := store.GetEmailCode(ctx, s.Store.Pool, email); err == nil && time.Since(prev.SentAt) < 60*time.Second {
		return userErr("код уже отправлен, повторить можно через минуту")
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	code := fmt.Sprintf("%06d", n.Int64())
	ttl := time.Duration(s.set().Auth.OTPTTLSeconds) * time.Second
	if err := store.PutEmailCode(ctx, s.Store.Pool, email, codeHash(email, code, s), ttl); err != nil {
		return err
	}
	shop := s.set().General.ShopName
	html := fmt.Sprintf(`<div style="font-family:sans-serif;font-size:16px">
<p>Ваш код для входа в %s:</p><p style="font-size:32px;font-weight:bold;letter-spacing:6px">%s</p>
<p style="color:#888">Код действует %d мин. Если вы не запрашивали вход — просто проигнорируйте письмо.</p></div>`,
		shop, code, int(ttl.Minutes()))
	if err := s.Mailer.Send(ctx, email, "Код входа "+shop+": "+code, html); err != nil {
		s.Log.Error("send otp", "err", err)
		return userErr("не удалось отправить письмо, попробуйте позже")
	}
	return nil
}

func codeHash(email, code string, s *Service) []byte {
	return []byte(s.Box.Sign("otp|" + email + "|" + code))
}

func (s *Service) VerifyEmailCode(ctx context.Context, rawEmail, code string, meta LoginMeta) (*Session, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return nil, err
	}
	code = strings.TrimSpace(code)
	rec, err := store.GetEmailCode(ctx, s.Store.Pool, email)
	if err != nil || time.Now().After(rec.ExpiresAt) {
		return nil, userErr("код истёк, запросите новый")
	}
	if rec.Attempts >= s.set().Auth.OTPMaxAttempts {
		return nil, userErr("слишком много попыток, запросите новый код")
	}
	if subtle.ConstantTimeCompare(rec.CodeHash, codeHash(email, code, s)) != 1 {
		_ = store.BumpEmailAttempts(ctx, s.Store.Pool, email)
		return nil, userErr("неверный код")
	}
	_ = store.DeleteEmailCode(ctx, s.Store.Pool, email)
	return s.Login(ctx, &auth.Profile{Provider: "email", Subject: email, Email: email, Display: email}, meta)
}

// ---- OAuth (Yandex, VK) ----

func (s *Service) oauthRedirect(provider string) string {
	return s.Cfg.PublicURL + "/api/auth/oauth/" + provider + "/callback"
}

// OAuthStart returns the provider URL to redirect the browser to.
func (s *Service) OAuthStart(ctx context.Context, provider string, linkTo *int64, returnTo string) (string, error) {
	a := s.set().Auth
	st := store.OAuthState{State: auth.RandomToken(24), Provider: provider, LinkTo: linkTo, ReturnTo: safeReturn(returnTo)}
	var url string
	switch provider {
	case "yandex":
		if !a.YandexEnabled {
			return "", userErr("вход через Яндекс отключён")
		}
		url = auth.Yandex{ClientID: a.YandexClientID, Secret: a.YandexSecret, RedirectURI: s.oauthRedirect("yandex")}.AuthURL(st.State)
	case "vk":
		if !a.VKEnabled {
			return "", userErr("вход через VK отключён")
		}
		st.Verifier = auth.RandomToken(48)
		url = auth.VK{ClientID: a.VKClientID, Secret: a.VKSecret, RedirectURI: s.oauthRedirect("vk")}.AuthURL(st.State, st.Verifier)
	default:
		return "", userErr("неизвестный способ входа")
	}
	if err := store.PutOAuthState(ctx, s.Store.Pool, st); err != nil {
		return "", err
	}
	return url, nil
}

// OAuthCallback finishes the provider flow. returnTo is where the browser should land.
func (s *Service) OAuthCallback(ctx context.Context, provider, code, state, deviceID string, meta LoginMeta) (*Session, string, error) {
	st, err := store.TakeOAuthState(ctx, s.Store.Pool, state)
	if err != nil || st.Provider != provider {
		return nil, "/", userErr("ссылка входа устарела, попробуйте ещё раз")
	}
	a := s.set().Auth
	var p *auth.Profile
	switch provider {
	case "yandex":
		p, err = auth.Yandex{ClientID: a.YandexClientID, Secret: a.YandexSecret, RedirectURI: s.oauthRedirect("yandex")}.Exchange(ctx, code)
	case "vk":
		p, err = auth.VK{ClientID: a.VKClientID, Secret: a.VKSecret, RedirectURI: s.oauthRedirect("vk")}.Exchange(ctx, code, deviceID, state, st.Verifier)
	default:
		err = errors.New("unknown provider")
	}
	if err != nil {
		s.Log.Warn("oauth exchange", "provider", provider, "err", err)
		return nil, st.ReturnTo, userErr("не удалось войти, попробуйте ещё раз")
	}
	meta.LinkTo = st.LinkTo
	sess, err := s.Login(ctx, p, meta)
	return sess, st.ReturnTo, err
}

func safeReturn(r string) string {
	if !strings.HasPrefix(r, "/") || strings.HasPrefix(r, "//") {
		return "/"
	}
	return r
}

// UnlinkIdentity removes a login method, keeping at least one.
func (s *Service) UnlinkIdentity(ctx context.Context, customerID, identityID int64) error {
	ids, err := store.ListIdentities(ctx, s.Store.Pool, customerID)
	if err != nil {
		return err
	}
	if len(ids) <= 1 {
		return userErr("нельзя отвязать последний способ входа")
	}
	_, err = store.DeleteIdentity(ctx, s.Store.Pool, customerID, identityID)
	return err
}
