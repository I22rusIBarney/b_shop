package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"bshop/internal/store"
)

// GiftRequest buys a gift code (Recipient empty) or a subscription for another customer (Recipient set).
type GiftRequest struct {
	Recipient string `json:"recipient"` // Telegram ID, @username or email — exact match
	PlanID    int64  `json:"plan_id"`
	PeriodID  int64  `json:"period_id"`
	Method    string `json:"method"`
	ReturnURL string `json:"return_url"`
}

// Recipient is what the payer sees about the person they pay for: enough to recognise them, nothing more.
type Recipient struct {
	Name string `json:"name"`
}

// Unambiguous alphabet: no 0/O, 1/I/L.
const giftAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newGiftCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = giftAlphabet[int(b[i])%len(giftAlphabet)]
	}
	return "GIFT-" + string(b[:4]) + "-" + string(b[4:]), nil
}

// maskName shows the first name, or a username/email with most characters hidden.
func maskName(c *store.Customer) string {
	if c.FirstName != nil && strings.TrimSpace(*c.FirstName) != "" {
		return strings.TrimSpace(*c.FirstName)
	}
	src := ""
	if c.Username != nil && *c.Username != "" {
		src = "@" + *c.Username
	} else if c.Email != nil {
		src = string(*c.Email)
	}
	if src == "" {
		return "пользователь"
	}
	r := []rune(src)
	keep := 3
	if len(r) <= keep {
		return src
	}
	return string(r[:keep]) + strings.Repeat("•", min(utf8.RuneCountInString(src)-keep, 6))
}

func (s *Service) giftPlan(ctx context.Context, c *store.Customer, planID, periodID int64) (*store.Plan, *store.Period, error) {
	plan, err := store.GetPlan(ctx, s.Store.Pool, planID)
	if err != nil {
		return nil, nil, userErr("тариф не найден")
	}
	per := plan.Period(periodID)
	if per == nil {
		return nil, nil, userErr("выберите срок подписки")
	}
	if !plan.IsActive || plan.IsTrial || s.planHidden(ctx, c, plan) {
		return nil, nil, userErr("этот тариф сейчас недоступен")
	}
	return plan, per, nil
}

func (s *Service) giftPayment(ctx context.Context, c *store.Customer, plan *store.Plan, per *store.Period, op, method string) *store.Payment {
	dim := s.set().General.DaysInMonth
	disc, code := s.discountFor(ctx, s.Store.Pool, c, plan.ID)
	return &store.Payment{
		CustomerID: c.ID, PlanID: &plan.ID, PeriodID: &per.ID, Operation: op,
		Title: plan.Name, Days: per.Days(dim), Months: per.Months(dim),
		Amount: applyDiscount(per.Price, disc), BaseAmount: per.Price, DiscountPercent: disc, PromoCode: code,
		Provider: "yookassa", Method: method,
	}
}

// FindRecipient checks who a purchase for another customer would go to.
func (s *Service) FindRecipient(ctx context.Context, c *store.Customer, query string) (*store.Customer, error) {
	if !s.set().Gifts.BuyForOther {
		return nil, userErr("покупка для другого отключена")
	}
	if strings.TrimSpace(query) == "" {
		return nil, userErr("укажите Telegram ID, @username или email")
	}
	r, err := store.FindCustomerExact(ctx, s.Store.Pool, query)
	if errors.Is(err, store.ErrNotFound) {
		return nil, userErr("пользователь не найден — проверьте Telegram ID, @username или email")
	}
	if err != nil {
		return nil, err
	}
	if r.ID == c.ID {
		return nil, userErr("это ваш аккаунт — оформите подписку себе в разделе покупки")
	}
	return r, nil
}

func (s *Service) LookupRecipient(ctx context.Context, c *store.Customer, query string) (*Recipient, error) {
	r, err := s.FindRecipient(ctx, c, query)
	if err != nil {
		return nil, err
	}
	return &Recipient{Name: maskName(r)}, nil
}

// BuyForOther: the payer pays, the recipient gets a new subscription (operation buy_for).
func (s *Service) BuyForOther(ctx context.Context, c *store.Customer, req GiftRequest) (*BuyResult, error) {
	rcpt, err := s.FindRecipient(ctx, c, req.Recipient)
	if err != nil {
		return nil, err
	}
	plan, per, err := s.giftPlan(ctx, c, req.PlanID, req.PeriodID)
	if err != nil {
		return nil, err
	}
	if err := s.checkNewSubscriptionAllowed(ctx, rcpt, plan); err != nil {
		return nil, userErr("получатель не может получить этот тариф: " + err.Error())
	}
	p := s.giftPayment(ctx, c, plan, per, "buy_for", req.Method)
	p.RecipientID = &rcpt.ID
	return s.startPayment(ctx, c, p, req.ReturnURL)
}

// BuyGift: the payer gets a code that anyone can activate once (operation gift).
func (s *Service) BuyGift(ctx context.Context, c *store.Customer, req GiftRequest) (*BuyResult, error) {
	if !s.set().Gifts.Enabled {
		return nil, userErr("подарочные подписки отключены")
	}
	plan, per, err := s.giftPlan(ctx, c, req.PlanID, req.PeriodID)
	if err != nil {
		return nil, err
	}
	return s.startPayment(ctx, c, s.giftPayment(ctx, c, plan, per, "gift", req.Method), req.ReturnURL)
}

// createGift runs inside ApplyPayment's transaction for a paid gift.
func createGift(ctx context.Context, tx pgx.Tx, buyer *store.Customer, pay *store.Payment, plan *store.Plan) (*store.Gift, error) {
	pid := pay.ID
	for range 5 {
		code, err := newGiftCode()
		if err != nil {
			return nil, err
		}
		g := &store.Gift{Code: code, BuyerID: buyer.ID, PaymentID: &pid, PlanID: &plan.ID, Title: plan.Name, Days: pay.Days}
		// A savepoint keeps the outer transaction usable if the random code collides.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, err
		}
		if err = store.InsertGift(ctx, sp, g); err == nil {
			return g, sp.Commit(ctx)
		}
		_ = sp.Rollback(ctx)
		if !strings.Contains(err.Error(), "gifts_code_key") {
			return nil, err
		}
	}
	return nil, errors.New("could not generate a unique gift code")
}

func (s *Service) ListGifts(ctx context.Context, c *store.Customer) ([]store.Gift, error) {
	return store.ListGiftsByBuyer(ctx, s.Store.Pool, c.ID)
}

// RedeemGift activates a gift code: the customer gets a new subscription of the gifted plan and length.
func (s *Service) RedeemGift(ctx context.Context, c *store.Customer, code string) (*store.Subscription, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, userErr("введите код подарка")
	}
	unlock := s.lock("gift:" + code)
	defer unlock()
	var (
		sub  *store.Subscription
		gift *store.Gift
	)
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		gift, err = store.GetGiftByCodeForUpdate(ctx, tx, code)
		if errors.Is(err, store.ErrNotFound) {
			return userErr("подарок с таким кодом не найден")
		}
		if err != nil {
			return err
		}
		if gift.Status != "ready" {
			return userErr("этот подарок уже активирован")
		}
		if gift.PlanID == nil {
			return userErr("тариф подарка больше не существует — напишите в поддержку")
		}
		plan, err := store.GetPlan(ctx, tx, *gift.PlanID)
		if err != nil {
			return userErr("тариф подарка больше не существует — напишите в поддержку")
		}
		cust, err := store.GetCustomerForUpdate(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if err := s.checkNewSubscriptionAllowed(ctx, cust, plan); err != nil {
			return err
		}
		sub, err = s.provisionNew(ctx, tx, cust, Grant{Plan: plan, Kind: "paid", Days: gift.Days,
			Username: fmt.Sprintf("u%d_g%d", cust.ID, gift.ID)})
		if err != nil {
			return err
		}
		if err := store.MarkGiftRedeemed(ctx, tx, gift.ID, cust.ID, sub.ID); err != nil {
			return err
		}
		return store.AddEvent(ctx, tx, cust.ID, "gift_redeemed", map[string]any{"gift": gift.ID, "plan": gift.Title,
			"days": gift.Days, "subscription": sub.ID}, nil)
	})
	if err != nil {
		return nil, err
	}
	until := sub.ExpiresAt.In(s.set().General.Location()).Format("02.01.2006")
	s.Notify.Customer(ctx, c, Message{
		Text:       fmt.Sprintf("🎁 Подарок активирован!\n\nПодписка <b>%s</b> действует до <b>%s</b>.", escape(sub.Title), until),
		ButtonText: "Открыть кабинет", ButtonURL: s.Cfg.PublicURL + "/", EmailSubject: "Подарок активирован",
	})
	if buyer, err := store.GetCustomer(ctx, s.Store.Pool, gift.BuyerID); err == nil && buyer.ID != c.ID {
		s.Notify.Customer(ctx, buyer, Message{Text: fmt.Sprintf("🎁 Ваш подарок <b>%s</b> активирован.", escape(gift.Title)),
			EmailSubject: "Подарок активирован"})
	}
	return sub, nil
}

// afterGiftPurchase tells the payer (and the recipient) what was bought.
func (s *Service) afterGiftPurchase(ctx context.Context, payer, rcpt *store.Customer, p *store.Payment, sub *store.Subscription, gift *store.Gift) {
	switch {
	case gift != nil:
		s.Notify.Customer(ctx, payer, Message{
			Text: fmt.Sprintf("✅ Оплата получена!\n\nПодарок <b>%s</b> на %s готов.\nКод: <code>%s</code>\n\nПерешлите код или ссылку получателю.",
				escape(gift.Title), periodLabel(p.Days, p.Months), gift.Code),
			ButtonText: "Мои подарки", ButtonURL: s.Cfg.PublicURL + "/gifts", EmailSubject: "Подарок оплачен",
		})
	case rcpt != nil && sub != nil:
		until := sub.ExpiresAt.In(s.set().General.Location()).Format("02.01.2006")
		s.Notify.Customer(ctx, payer, Message{
			Text:         fmt.Sprintf("✅ Оплата получена!\n\nПодписка <b>%s</b> оформлена для %s до <b>%s</b>.", escape(sub.Title), escape(maskName(rcpt)), until),
			EmailSubject: "Оплата получена",
		})
		s.Notify.Customer(ctx, rcpt, Message{
			Text:       fmt.Sprintf("🎁 Вам оплатили подписку <b>%s</b> до <b>%s</b>.", escape(sub.Title), until),
			ButtonText: "Открыть кабинет", ButtonURL: s.Cfg.PublicURL + "/", EmailSubject: "Вам оплатили подписку",
		})
	}
	op := map[string]string{"gift": "Подарок", "buy_for": "Покупка другому"}[p.Operation]
	s.event(ctx, "purchase", payer, map[string]any{"operation": op, "plan": p.Title, "detail": periodLabel(p.Days, p.Months),
		"amount": rub(p.Amount), "promo": Raw("")})
}
