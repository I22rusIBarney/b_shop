package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"bshop/internal/store"
	"bshop/internal/yookassa"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CatalogPlan is a plan as shown to a specific customer (prices after personal discount).
type CatalogPlan struct {
	store.Plan
	PricePerMonth int64         `json:"price_per_month"`
	Offers        []PeriodOffer `json:"offers"`
}

type PeriodOffer struct {
	PeriodID  int64  `json:"period_id"`
	Unit      string `json:"unit"`
	Amount    int    `json:"amount"`
	Days      int    `json:"days"`
	BasePrice int64  `json:"base_price"`
	Price     int64  `json:"price"`
	PerMonth  int64  `json:"per_month"`
	IsBest    bool   `json:"is_best"`
	FullPrice int64  `json:"full_price"` // price of the same days at the shortest period's rate (for strike-through)
}

func applyDiscount(price int64, percent int) int64 {
	if percent <= 0 {
		return price
	}
	if percent >= 100 {
		return 0
	}
	return price * int64(100-percent) / 100
}

// Catalog lists plans the customer can buy now.
func (s *Service) Catalog(ctx context.Context, c *store.Customer) ([]CatalogPlan, error) {
	plans, err := store.ListPlans(ctx, s.Store.Pool)
	if err != nil {
		return nil, err
	}
	subs, err := store.ListSubscriptions(ctx, s.Store.Pool, c.ID)
	if err != nil {
		return nil, err
	}
	owned := map[int64]bool{}
	for _, sb := range subs {
		if sb.PlanID != nil {
			owned[*sb.PlanID] = true
		}
	}
	dim := s.set().General.DaysInMonth
	out := []CatalogPlan{}
	for _, p := range plans {
		if !p.IsActive || p.IsTrial || len(p.Periods) == 0 {
			continue
		}
		if p.IsHidden && !owned[p.ID] {
			continue
		}
		out = append(out, s.catalogPlan(p, c.PersonalDiscount, dim))
	}
	return out, nil
}

func (s *Service) catalogPlan(p store.Plan, discount, dim int) CatalogPlan {
	cp := CatalogPlan{Plan: p}
	monthly := p.MonthlyPrice(dim)
	for _, per := range p.Periods {
		d := per.Days(dim)
		price := applyDiscount(per.Price, discount)
		o := PeriodOffer{PeriodID: per.ID, Unit: per.Unit, Amount: per.Amount, Days: d, BasePrice: per.Price,
			Price: price, IsBest: per.IsBest}
		if d > 0 {
			o.PerMonth = price * int64(dim) / int64(d)
			o.FullPrice = applyDiscount(monthly*int64(d)/int64(dim), discount)
		}
		if o.FullPrice < price {
			o.FullPrice = price
		}
		cp.Offers = append(cp.Offers, o)
		if cp.PricePerMonth == 0 || (o.PerMonth > 0 && o.PerMonth < cp.PricePerMonth) {
			cp.PricePerMonth = o.PerMonth
		}
	}
	return cp
}

type BuyRequest struct {
	PlanID         int64  `json:"plan_id"`
	PeriodID       int64  `json:"period_id"`
	SubscriptionID int64  `json:"subscription_id"` // renew this subscription; 0 = new subscription
	Method         string `json:"method"`          // yookassa method code, e.g. sbp
	SaveCard       bool   `json:"save_card"`
	ReturnURL      string `json:"return_url"`
}

type BuyResult struct {
	PaymentID       uuid.UUID `json:"payment_id"`
	ConfirmationURL string    `json:"confirmation_url"`
	Amount          int64     `json:"amount"`
	Applied         bool      `json:"applied"` // free (100% discount) purchases complete immediately
}

// Buy validates a purchase, creates a local payment and a YooKassa payment.
func (s *Service) Buy(ctx context.Context, c *store.Customer, req BuyRequest) (*BuyResult, error) {
	plan, err := store.GetPlan(ctx, s.Store.Pool, req.PlanID)
	if err != nil {
		return nil, userErr("тариф не найден")
	}
	per := plan.Period(req.PeriodID)
	if per == nil {
		return nil, userErr("выберите срок подписки")
	}
	if !plan.IsActive || plan.IsTrial {
		return nil, userErr("этот тариф сейчас недоступен")
	}
	op := "buy"
	var sub *store.Subscription
	if req.SubscriptionID != 0 {
		sub, err = store.GetSubscription(ctx, s.Store.Pool, req.SubscriptionID)
		if err != nil || sub.CustomerID != c.ID {
			return nil, userErr("подписка не найдена")
		}
		if sub.PlanID == nil || *sub.PlanID != plan.ID {
			return nil, userErr("для другого тарифа используйте смену тарифа")
		}
		if plan.RenewalDisabled {
			return nil, userErr("продление этого тарифа отключено")
		}
		op = "renew"
	} else {
		if plan.IsHidden {
			return nil, userErr("этот тариф сейчас недоступен")
		}
		if err := s.checkNewSubscriptionAllowed(ctx, c, plan); err != nil {
			return nil, err
		}
	}

	dim := s.set().General.DaysInMonth
	amount := applyDiscount(per.Price, c.PersonalDiscount)
	p := &store.Payment{
		CustomerID: c.ID, PlanID: &plan.ID, PeriodID: &per.ID, Operation: op,
		Title: plan.Name, Days: per.Days(dim), Months: per.Months(dim),
		Amount: amount, BaseAmount: per.Price, DiscountPercent: c.PersonalDiscount,
		Provider: "yookassa", Method: req.Method,
		SaveMethod: req.SaveCard && s.set().YooKassa.SaveCard,
	}
	if sub != nil {
		p.SubscriptionID = &sub.ID
	}
	return s.startPayment(ctx, c, p, req.ReturnURL)
}

func (s *Service) checkNewSubscriptionAllowed(ctx context.Context, c *store.Customer, plan *store.Plan) error {
	if plan.RequiredPlanID != nil {
		subs, err := store.ListSubscriptions(ctx, s.Store.Pool, c.ID)
		if err != nil {
			return err
		}
		ok := false
		for _, sb := range subs {
			if sb.PlanID != nil && *sb.PlanID == *plan.RequiredPlanID && sb.Active() {
				ok = true
			}
		}
		if !ok {
			req, _ := store.GetPlan(ctx, s.Store.Pool, *plan.RequiredPlanID)
			name := "другой тариф"
			if req != nil {
				name = "тариф «" + req.Name + "»"
			}
			return userErr("для покупки нужен активный " + name)
		}
	}
	if plan.MaxPerCustomer > 0 {
		n, err := store.CountSubscriptions(ctx, s.Store.Pool, c.ID, &plan.ID)
		if err != nil {
			return err
		}
		if n >= plan.MaxPerCustomer {
			return userErr("достигнут лимит подписок этого тарифа — продлите существующую")
		}
	}
	if max := s.set().General.MaxSubsPerCustomer; max > 0 {
		n, err := store.CountSubscriptions(ctx, s.Store.Pool, c.ID, nil)
		if err != nil {
			return err
		}
		if n >= max {
			return userErr("достигнут лимит подписок — продлите существующую")
		}
	}
	return nil
}

func (s *Service) startPayment(ctx context.Context, c *store.Customer, p *store.Payment, returnURL string) (*BuyResult, error) {
	y := s.set().YooKassa
	if p.Amount == 0 {
		p.Provider = "free"
		if err := store.InsertPayment(ctx, s.Store.Pool, p); err != nil {
			return nil, err
		}
		if _, err := store.MarkPaid(ctx, s.Store.Pool, p.ID, "free"); err != nil {
			return nil, err
		}
		if err := s.ApplyPayment(ctx, p.ID); err != nil {
			return nil, err
		}
		return &BuyResult{PaymentID: p.ID, Applied: true}, nil
	}
	gw, err := s.gateway()
	if err != nil {
		return nil, err
	}
	if p.Method == "" && len(y.Methods) > 0 {
		p.Method = y.Methods[0]
	}
	allowed := false
	for _, m := range y.Methods {
		if m == p.Method {
			allowed = true
		}
	}
	if !allowed {
		return nil, userErr("выберите способ оплаты")
	}
	if y.CommissionPercent > 0 {
		p.Amount += (p.Amount*int64(y.CommissionPercent*100) + 9999) / 10000
	}
	if err := store.InsertPayment(ctx, s.Store.Pool, p); err != nil {
		return nil, err
	}
	ret := s.paymentReturnURL(p.ID, returnURL)
	in := yookassa.CreatePayment{
		Amount:            yookassa.Kopecks(p.Amount),
		Capture:           true,
		Confirmation:      &yookassa.Confirmation{Type: "redirect", ReturnURL: ret},
		Description:       s.paymentDescription(p),
		Metadata:          map[string]string{"payment_id": p.ID.String(), "customer_id": strconv.FormatInt(c.ID, 10)},
		PaymentMethodData: &yookassa.MethodData{Type: p.Method},
		SavePaymentMethod: p.SaveMethod && p.Method == "bank_card",
	}
	yp, err := gw.CreatePayment(ctx, p.ID.String(), in)
	if err != nil {
		s.Log.Error("yookassa create", "payment", p.ID, "err", err)
		_ = store.MarkCanceled(ctx, s.Store.Pool, p.ID, "provider_error")
		s.Notify.Admin(ctx, "payment_error", fmt.Sprintf("⚠️ Ошибка создания платежа ЮKassa: %v", err), c.ID)
		return nil, userErr("не удалось создать платёж, попробуйте другой способ или позже")
	}
	url := ""
	if yp.Confirmation != nil {
		url = yp.Confirmation.ConfirmationURL
	}
	if err := store.SetPaymentProvider(ctx, s.Store.Pool, p.ID, yp.ID, url); err != nil {
		return nil, err
	}
	_ = store.AddEvent(ctx, s.Store.Pool, c.ID, "payment_created", map[string]any{"payment": p.ID, "amount": p.Amount, "plan": p.Title, "op": p.Operation}, nil)
	return &BuyResult{PaymentID: p.ID, ConfirmationURL: url, Amount: p.Amount}, nil
}

func (s *Service) paymentReturnURL(id uuid.UUID, requested string) string {
	if u := s.set().Payments.SuccessURL; u != "" {
		return u
	}
	if requested != "" && strings.HasPrefix(requested, s.Cfg.PublicURL+"/") {
		return requested
	}
	return s.Cfg.PublicURL + "/payment/" + id.String()
}

func periodLabel(days, months int) string {
	if months > 0 && days >= 28 {
		return plural(months, "месяц", "месяца", "месяцев")
	}
	return plural(days, "день", "дня", "дней")
}

func plural(n int, one, few, many string) string {
	w := many
	switch {
	case n%10 == 1 && n%100 != 11:
		w = one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		w = few
	}
	return fmt.Sprintf("%d %s", n, w)
}

func (s *Service) paymentDescription(p *store.Payment) string {
	d := strings.NewReplacer("{plan}", p.Title, "{period}", periodLabel(p.Days, p.Months),
		"{days}", strconv.Itoa(p.Days), "{months}", strconv.Itoa(p.Months)).Replace(s.set().YooKassa.DescriptionTemplate)
	if d == "" {
		d = "Подписка " + p.Title
	}
	if r := []rune(d); len(r) > 128 {
		d = string(r[:128])
	}
	return d
}

// ---- payment confirmation ----

// HandleYooKassaNotification re-reads the object from the API and applies the result.
func (s *Service) HandleYooKassaNotification(ctx context.Context, n yookassa.Notification) error {
	gw, err := s.gateway()
	if err != nil {
		return err
	}
	switch n.Event {
	case "payment.succeeded", "payment.canceled", "payment.waiting_for_capture":
		yp, err := gw.GetPayment(ctx, n.Object.ID)
		if err != nil {
			return err
		}
		return s.syncProviderPayment(ctx, yp)
	case "refund.succeeded":
		r, err := gw.GetRefund(ctx, n.Object.ID)
		if err != nil {
			return err
		}
		if r.Status != "succeeded" {
			return nil
		}
		p, err := store.GetPaymentByProviderRef(ctx, s.Store.Pool, "yookassa", r.PaymentID)
		if err != nil {
			return nil
		}
		return s.onRefund(ctx, p, r.Amount.ToKopecks())
	}
	return nil
}

func (s *Service) syncProviderPayment(ctx context.Context, yp *yookassa.Payment) error {
	p, err := store.GetPaymentByProviderRef(ctx, s.Store.Pool, "yookassa", yp.ID)
	if errors.Is(err, store.ErrNotFound) {
		if id, perr := uuid.Parse(yp.Metadata["payment_id"]); perr == nil {
			p, err = store.GetPayment(ctx, s.Store.Pool, id)
		}
	}
	if err != nil {
		return fmt.Errorf("payment for %s: %w", yp.ID, err)
	}
	switch yp.Status {
	case "succeeded":
		if got := yp.Amount.ToKopecks(); got < p.Amount {
			s.Notify.Admin(ctx, "payment_error", fmt.Sprintf("⚠️ Сумма платежа %s меньше ожидаемой: %d < %d", yp.ID, got, p.Amount), p.CustomerID)
			return nil
		}
		method := p.Method
		if yp.PaymentMethod != nil {
			method = yp.PaymentMethod.Type
			if yp.PaymentMethod.Saved && yp.PaymentMethod.ID != "" {
				title := yp.PaymentMethod.Title
				if title == "" && yp.PaymentMethod.Card != nil {
					title = yp.PaymentMethod.Card.CardType + " *" + yp.PaymentMethod.Card.Last4
				}
				_ = store.SavePaymentMethod(ctx, s.Store.Pool, p.CustomerID, "yookassa", yp.PaymentMethod.ID, title)
			}
		}
		if _, err := store.MarkPaid(ctx, s.Store.Pool, p.ID, method); err != nil {
			return err
		}
		return s.ApplyPayment(ctx, p.ID)
	case "canceled":
		reason := ""
		if yp.CancellationDetails != nil {
			reason = yp.CancellationDetails.Reason
		}
		return store.MarkCanceled(ctx, s.Store.Pool, p.ID, reason)
	}
	return nil
}

// CheckPayment lets the cabinet poll a payment; it asks YooKassa directly if still pending.
func (s *Service) CheckPayment(ctx context.Context, c *store.Customer, id uuid.UUID) (*store.Payment, error) {
	p, err := store.GetPayment(ctx, s.Store.Pool, id)
	if err != nil || (p.CustomerID != c.ID && !c.IsAdmin) {
		return nil, userErr("платёж не найден")
	}
	if p.Status == "pending" && p.ProviderRef != nil {
		if gw, err := s.gateway(); err == nil {
			if yp, err := gw.GetPayment(ctx, *p.ProviderRef); err == nil {
				_ = s.syncProviderPayment(ctx, yp)
				p, _ = store.GetPayment(ctx, s.Store.Pool, id)
			}
		}
	}
	return p, nil
}

// ApplyPayment turns a succeeded payment into subscription changes exactly once.
func (s *Service) ApplyPayment(ctx context.Context, id uuid.UUID) error {
	unlock := s.lock("pay:" + id.String())
	defer unlock()

	var (
		cust *store.Customer
		pay  *store.Payment
		sub  *store.Subscription
	)
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		pay, err = store.GetPaymentForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if pay.Status != "succeeded" || pay.AppliedAt != nil {
			pay = nil
			return nil
		}
		cust, err = store.GetCustomerForUpdate(ctx, tx, pay.CustomerID)
		if err != nil {
			return err
		}
		if pay.PlanID == nil {
			return errors.New("payment without plan")
		}
		plan, err := store.GetPlan(ctx, tx, *pay.PlanID)
		if err != nil {
			return err
		}
		g := Grant{Plan: plan, Kind: "paid", Days: pay.Days, ExtraDevices: pay.ExtraDevices,
			Username: fmt.Sprintf("u%d_%s", cust.ID, strings.ReplaceAll(pay.ID.String(), "-", "")[:10])}
		switch pay.Operation {
		case "buy":
			sub, err = s.provisionNew(ctx, tx, cust, g)
		case "renew", "autopay":
			if pay.SubscriptionID == nil {
				sub, err = s.provisionNew(ctx, tx, cust, g)
				break
			}
			sub, err = store.GetSubscriptionForUpdate(ctx, tx, *pay.SubscriptionID)
			if err == nil {
				err = s.provisionExtend(ctx, tx, cust, sub, g)
			}
		default:
			err = fmt.Errorf("operation %s not supported yet", pay.Operation)
		}
		if err != nil {
			return err
		}
		if err := store.MarkApplied(ctx, tx, pay.ID, sub.ID); err != nil {
			return err
		}
		if s.set().MoyNalog.Enabled && pay.Provider != "free" {
			if err := store.QueueReceipt(ctx, tx, pay.ID); err != nil {
				return err
			}
		}
		return store.AddEvent(ctx, tx, cust.ID, "purchase", map[string]any{"payment": pay.ID, "op": pay.Operation,
			"plan": pay.Title, "days": pay.Days, "amount": pay.Amount, "subscription": sub.ID}, nil)
	})
	if err != nil {
		s.Log.Error("apply payment", "payment", id, "err", err)
		s.Notify.Admin(ctx, "payment_error", fmt.Sprintf("⚠️ Оплата %s получена, но подписка не выдана: %v. Повторю автоматически.", id, err), 0)
		return err
	}
	if pay == nil {
		return nil
	}
	s.afterPurchase(ctx, cust, pay, sub)
	return nil
}

func rub(k int64) string {
	if k%100 == 0 {
		return fmt.Sprintf("%d ₽", k/100)
	}
	return fmt.Sprintf("%d.%02d ₽", k/100, k%100)
}

func (s *Service) afterPurchase(ctx context.Context, c *store.Customer, p *store.Payment, sub *store.Subscription) {
	loc := s.set().General.Location()
	verb := "оформлена"
	if p.Operation != "buy" {
		verb = "продлена"
	}
	s.Notify.Customer(ctx, c, Message{
		Text: fmt.Sprintf("✅ Оплата получена!\n\nПодписка <b>%s</b> %s до <b>%s</b>.", escape(sub.Title), verb,
			sub.ExpiresAt.In(loc).Format("02.01.2006")),
		ButtonText:   "Открыть кабинет",
		ButtonURL:    s.Cfg.PublicURL + "/",
		EmailSubject: "Оплата получена",
	})
	who := c.DisplayName()
	if c.TelegramID != nil {
		who += fmt.Sprintf(" (%d)", *c.TelegramID)
	}
	opName := map[string]string{"buy": "Покупка", "renew": "Продление", "autopay": "Автопродление"}[p.Operation]
	s.Notify.Admin(ctx, "purchase", fmt.Sprintf("🛒 %s\n👤 %s\n📦 %s · %s\n💰 %s", opName, escape(who), escape(p.Title),
		periodLabel(p.Days, p.Months), rub(p.Amount)), c.ID)
}

func (s *Service) onRefund(ctx context.Context, p *store.Payment, amount int64) error {
	if err := store.MarkRefunded(ctx, s.Store.Pool, p.ID); err != nil {
		return err
	}
	if err := store.QueueReceiptCancel(ctx, s.Store.Pool, p.ID); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, s.Store.Pool, p.CustomerID, "refund", map[string]any{"payment": p.ID, "amount": amount}, nil)
	s.Notify.Admin(ctx, "refund", fmt.Sprintf("↩️ Возврат %s по платежу %s", rub(amount), p.ID), p.CustomerID)
	return nil
}

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
