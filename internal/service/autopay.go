package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"bshop/internal/store"
	"bshop/internal/yookassa"
)

// autopayQuote returns the plan period and amount an autopay renewal would charge.
func (s *Service) autopayQuote(ctx context.Context, c *store.Customer, sub *store.Subscription) (*store.Plan, *store.Period, int64, int64, error) {
	if sub.PlanID == nil {
		return nil, nil, 0, 0, userErr("у подписки нет тарифа")
	}
	plan, err := store.GetPlan(ctx, s.Store.Pool, *sub.PlanID)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	if plan.RenewalDisabled || !plan.IsActive || len(plan.Periods) == 0 {
		return nil, nil, 0, 0, userErr("продление этого тарифа недоступно")
	}
	var per *store.Period
	if sub.AutopayPeriodID != nil {
		per = plan.Period(*sub.AutopayPeriodID)
	}
	if per == nil {
		if id, _ := store.LastPaidPeriod(ctx, s.Store.Pool, sub.ID); id != nil {
			per = plan.Period(*id)
		}
	}
	dim := s.set().General.DaysInMonth
	if per == nil {
		per = &plan.Periods[0]
		for i := range plan.Periods {
			if plan.Periods[i].Days(dim) < per.Days(dim) {
				per = &plan.Periods[i]
			}
		}
	}
	base := per.Price
	if sub.ExtraDevices > 0 && plan.ExtraDevicesAllowed {
		base += plan.ExtraDevicePrice * int64(sub.ExtraDevices) * int64(per.Days(dim)) / int64(dim)
	}
	return plan, per, base, applyDiscount(base, c.PersonalDiscount), nil
}

// AutopayInfo is what the cabinet shows for a subscription's autopay.
type AutopayInfo struct {
	Available bool                  `json:"available"`
	Enabled   bool                  `json:"enabled"`
	MethodID  *int64                `json:"method_id"`
	Methods   []store.PaymentMethod `json:"methods"`
	PeriodID  int64                 `json:"period_id"`
	Days      int                   `json:"days"`
	Amount    int64                 `json:"amount"`
	ChargeAt  *time.Time            `json:"charge_at"`
	Reason    string                `json:"reason,omitempty"`
}

func (s *Service) Autopay(ctx context.Context, c *store.Customer, subID int64) (*AutopayInfo, error) {
	sub, err := store.GetSubscription(ctx, s.Store.Pool, subID)
	if err != nil || sub.CustomerID != c.ID {
		return nil, userErr("подписка не найдена")
	}
	pc := s.set().Payments
	info := &AutopayInfo{Enabled: sub.Autopay, MethodID: sub.AutopayMethodID}
	if info.Methods, err = store.ListPaymentMethods(ctx, s.Store.Pool, c.ID); err != nil {
		return nil, err
	}
	switch {
	case !pc.AutopayEnabled:
		info.Reason = "автопродление сейчас недоступно"
	case sub.Kind != "paid":
		info.Reason = "доступно на платной подписке"
	default:
		_, per, _, amount, err := s.autopayQuote(ctx, c, sub)
		if err != nil {
			info.Reason = err.Error()
			break
		}
		info.Available = true
		info.PeriodID, info.Days, info.Amount = per.ID, per.Days(s.set().General.DaysInMonth), amount
		at := sub.ExpiresAt.Add(-time.Duration(pc.AutopayDaysBefore) * 24 * time.Hour)
		info.ChargeAt = &at
	}
	return info, nil
}

// SetAutopay turns autopay on or off; turning it on needs a saved payment method.
func (s *Service) SetAutopay(ctx context.Context, c *store.Customer, subID int64, on bool, methodID int64) (*AutopayInfo, error) {
	info, err := s.Autopay(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	if !on {
		if err := store.SetAutopay(ctx, s.Store.Pool, subID, false, nil, nil); err != nil {
			return nil, err
		}
		_ = store.AddEvent(ctx, s.Store.Pool, c.ID, "autopay_off", map[string]any{"subscription": subID}, nil)
		return s.Autopay(ctx, c, subID)
	}
	if !info.Available {
		return nil, userErr(info.Reason)
	}
	var mid *int64
	for i := range info.Methods {
		m := info.Methods[i]
		if (methodID != 0 && m.ID == methodID) || (methodID == 0 && info.MethodID != nil && m.ID == *info.MethodID) {
			mid = &m.ID
		}
	}
	if mid == nil && methodID == 0 && len(info.Methods) > 0 {
		mid = &info.Methods[0].ID
	}
	if mid == nil {
		return nil, userErr("сначала оплатите подписку с сохранением способа оплаты")
	}
	pid := info.PeriodID
	if err := store.SetAutopay(ctx, s.Store.Pool, subID, true, mid, &pid); err != nil {
		return nil, err
	}
	_ = store.AddEvent(ctx, s.Store.Pool, c.ID, "autopay_on", map[string]any{"subscription": subID}, nil)
	return s.Autopay(ctx, c, subID)
}

// RunAutopay sends "will charge soon" notices and charges subscriptions that are due.
func (s *Service) RunAutopay(ctx context.Context) {
	pc := s.set().Payments
	if !pc.AutopayEnabled {
		return
	}
	loc := s.set().General.Location()
	if pc.AutopayNotifyDays > pc.AutopayDaysBefore {
		subs, err := store.AutopayToNotify(ctx, s.Store.Pool, time.Now().Add(time.Duration(pc.AutopayNotifyDays)*24*time.Hour))
		if err == nil {
			for i := range subs {
				sub := &subs[i]
				// Only warn while the charge is still ahead.
				if time.Until(sub.ExpiresAt) <= time.Duration(pc.AutopayDaysBefore)*24*time.Hour {
					_ = store.MarkAutopayNotified(ctx, s.Store.Pool, sub.ID, sub.ExpiresAt)
					continue
				}
				c, err := store.GetCustomer(ctx, s.Store.Pool, sub.CustomerID)
				if err != nil {
					continue
				}
				_, per, _, amount, err := s.autopayQuote(ctx, c, sub)
				if err != nil {
					continue
				}
				at := sub.ExpiresAt.Add(-time.Duration(pc.AutopayDaysBefore) * 24 * time.Hour).In(loc)
				s.Notify.Customer(ctx, c, Message{
					Text: fmt.Sprintf("🔁 %s спишем <b>%s</b> за продление подписки <b>%s</b> на %s.\n\nОтключить автопродление можно в кабинете.",
						at.Format("02.01.2006"), rub(amount), escape(sub.Title), periodLabel(per.Days(s.set().General.DaysInMonth), per.Months(s.set().General.DaysInMonth))),
					ButtonText: "Открыть кабинет", ButtonURL: s.Cfg.PublicURL + "/", EmailSubject: "Скоро автопродление",
				})
				_ = store.MarkAutopayNotified(ctx, s.Store.Pool, sub.ID, sub.ExpiresAt)
			}
		}
	}
	due, err := store.AutopayDue(ctx, s.Store.Pool, time.Now().Add(time.Duration(pc.AutopayDaysBefore)*24*time.Hour))
	if err != nil {
		s.Log.Error("autopay due", "err", err)
		return
	}
	for i := range due {
		if err := s.chargeAutopay(ctx, &due[i]); err != nil {
			s.Log.Warn("autopay", "sub", due[i].ID, "err", err)
		}
	}
}

// ChargeAutopayNow runs autopay for one subscription immediately (admin button).
func (s *Service) ChargeAutopayNow(ctx context.Context, subID int64) error {
	sub, err := store.GetSubscription(ctx, s.Store.Pool, subID)
	if err != nil {
		return userErr("подписка не найдена")
	}
	if !sub.Autopay || sub.AutopayMethodID == nil {
		return userErr("у подписки не включено автопродление")
	}
	return s.chargeAutopay(ctx, sub)
}

func (s *Service) chargeAutopay(ctx context.Context, sub *store.Subscription) error {
	unlock := s.lock(fmt.Sprintf("autopay:%d", sub.ID))
	defer unlock()
	if err := store.MarkAutopayTry(ctx, s.Store.Pool, sub.ID); err != nil {
		return err
	}
	c, err := store.GetCustomer(ctx, s.Store.Pool, sub.CustomerID)
	if err != nil {
		return err
	}
	method, err := store.GetPaymentMethod(ctx, s.Store.Pool, *sub.AutopayMethodID)
	if err != nil || method.CustomerID != c.ID {
		_ = store.SetAutopay(ctx, s.Store.Pool, sub.ID, false, nil, nil)
		return errors.New("autopay method missing")
	}
	plan, per, base, amount, err := s.autopayQuote(ctx, c, sub)
	if err != nil {
		_ = store.SetAutopay(ctx, s.Store.Pool, sub.ID, false, nil, nil)
		s.Notify.Customer(ctx, c, Message{Text: fmt.Sprintf("⚠️ Автопродление подписки <b>%s</b> отключено: %s. Продлите подписку вручную.",
			escape(sub.Title), err.Error()), ButtonText: "Открыть кабинет", ButtonURL: s.Cfg.PublicURL + "/", EmailSubject: "Автопродление отключено"})
		return err
	}
	gw, err := s.gateway()
	if err != nil {
		return err
	}
	dim := s.set().General.DaysInMonth
	p := &store.Payment{CustomerID: c.ID, SubscriptionID: &sub.ID, PlanID: &plan.ID, PeriodID: &per.ID, Operation: "autopay",
		Title: plan.Name, Days: per.Days(dim), Months: per.Months(dim), Amount: amount, BaseAmount: base,
		DiscountPercent: c.PersonalDiscount, Provider: "yookassa", Method: method.Provider, Autopay: true}
	if amount == 0 {
		return s.applyFree(ctx, p)
	}
	if err := store.InsertPayment(ctx, s.Store.Pool, p); err != nil {
		return err
	}
	yp, err := gw.CreatePayment(ctx, p.ID.String(), yookassa.CreatePayment{
		Amount:          yookassa.Kopecks(p.Amount),
		Capture:         true,
		Description:     s.paymentDescription(p),
		Metadata:        map[string]string{"payment_id": p.ID.String(), "customer_id": strconv.FormatInt(c.ID, 10), "autopay": "1"},
		PaymentMethodID: method.ProviderRef,
	})
	if err != nil {
		_ = store.MarkCanceled(ctx, s.Store.Pool, p.ID, "provider_error")
		s.autopayFailed(ctx, p, err.Error())
		return err
	}
	if err := store.SetPaymentProvider(ctx, s.Store.Pool, p.ID, yp.ID, ""); err != nil {
		return err
	}
	return s.syncProviderPayment(ctx, yp)
}

func (s *Service) applyFree(ctx context.Context, p *store.Payment) error {
	p.Provider = "free"
	if err := store.InsertPayment(ctx, s.Store.Pool, p); err != nil {
		return err
	}
	if _, err := store.MarkPaid(ctx, s.Store.Pool, p.ID, "free"); err != nil {
		return err
	}
	return s.ApplyPayment(ctx, p.ID)
}

var autopayReasons = map[string]string{
	"insufficient_funds":            "недостаточно средств",
	"card_expired":                  "истёк срок действия карты",
	"payment_method_restricted":     "способ оплаты недоступен",
	"permission_revoked":            "разрешение на списание отозвано",
	"payment_method_limit_exceeded": "превышен лимит",
	"expired_on_confirmation":       "платёж не подтверждён",
}

// autopayFailed tells the customer and admins that an automatic charge did not go through.
func (s *Service) autopayFailed(ctx context.Context, p *store.Payment, reason string) {
	c, err := store.GetCustomer(ctx, s.Store.Pool, p.CustomerID)
	if err != nil {
		return
	}
	human := autopayReasons[reason]
	if human == "" {
		human = "платёж отклонён"
	}
	if reason == "permission_revoked" && p.SubscriptionID != nil {
		_ = store.SetAutopay(ctx, s.Store.Pool, *p.SubscriptionID, false, nil, nil)
	}
	s.Notify.Customer(ctx, c, Message{
		Text: fmt.Sprintf("⚠️ Не удалось продлить подписку <b>%s</b> автоматически (%s, %s).\n\nПродлите её вручную, чтобы VPN не отключился.",
			escape(p.Title), rub(p.Amount), human),
		ButtonText: "Продлить", ButtonURL: s.Cfg.PublicURL + "/", EmailSubject: "Ошибка автопродления",
	})
	s.event(ctx, "autopay_error", c, map[string]any{"plan": p.Title, "amount": rub(p.Amount), "error": reason})
	_ = store.AddEvent(ctx, s.Store.Pool, c.ID, "autopay_failed", map[string]any{"payment": p.ID, "reason": reason}, nil)
}
