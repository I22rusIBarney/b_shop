package service

import (
	"context"
	"errors"
	"time"

	"bshop/internal/store"
	"bshop/internal/yookassa"

	"github.com/google/uuid"
)

// MaxReconcileDays limits one reconciliation run, so a request cannot page through years of payments.
const MaxReconcileDays = 31

// ReconcileItem is one YooKassa payment that needed attention.
type ReconcileItem struct {
	ProviderID string     `json:"provider_id"`
	PaymentID  *uuid.UUID `json:"payment_id,omitempty"`
	CustomerID int64      `json:"customer_id,omitempty"`
	Amount     int64      `json:"amount"`
	Note       string     `json:"note"`
}

// ReconcileReport compares succeeded YooKassa payments with local orders.
type ReconcileReport struct {
	From      time.Time       `json:"from"`
	To        time.Time       `json:"to"`
	Checked   int             `json:"checked"`   // succeeded payments at YooKassa in the period
	Recovered []ReconcileItem `json:"recovered"` // paid but not applied here — applied now
	Problems  []ReconcileItem `json:"problems"`  // still not applied (amount, missing plan…) — needs a person
	Unknown   []ReconcileItem `json:"unknown"`   // no local order at all (created outside the shop?)
}

// ReconcileYooKassa finds payments YooKassa took money for but the shop never applied (lost webhook,
// local expiry before a late payment, crash between steps) and applies them through the normal
// idempotent path. Already applied or refunded orders are left alone, so running it again is safe.
func (s *Service) ReconcileYooKassa(ctx context.Context, from, to time.Time) (*ReconcileReport, error) {
	if !to.After(from) {
		return nil, userErr("конец периода должен быть позже начала")
	}
	if to.Sub(from) > MaxReconcileDays*24*time.Hour {
		return nil, userErr("период сверки — не больше 31 дня")
	}
	gw, err := s.gateway()
	if errors.Is(err, ErrPaymentsDisabled) {
		return nil, userErr("ЮKassa не подключена — включите её в Настройки → Оплата и ЮKassa")
	} else if err != nil {
		return nil, err
	}
	rep := &ReconcileReport{From: from, To: to, Recovered: []ReconcileItem{}, Problems: []ReconcileItem{}, Unknown: []ReconcileItem{}}
	cursor := ""
	for page := 0; page < 100; page++ {
		l, err := gw.ListPayments(ctx, "succeeded", from, to, cursor)
		if err != nil {
			return nil, errors.New(s.Redact("ЮKassa: " + err.Error()))
		}
		for i := range l.Items {
			rep.Checked++
			s.reconcileOne(ctx, &l.Items[i], rep)
		}
		if l.NextCursor == "" || len(l.Items) == 0 {
			break
		}
		cursor = l.NextCursor
	}
	return rep, nil
}

func (s *Service) reconcileOne(ctx context.Context, yp *yookassa.Payment, rep *ReconcileReport) {
	item := ReconcileItem{ProviderID: yp.ID, Amount: yp.Amount.ToKopecks()}
	p, err := s.localPayment(ctx, yp)
	if err != nil {
		item.Note = "в магазине нет такого заказа"
		rep.Unknown = append(rep.Unknown, item)
		return
	}
	item.PaymentID, item.CustomerID = &p.ID, p.CustomerID
	if p.Status == "refunded" || (p.Status == "succeeded" && p.AppliedAt != nil) {
		return
	}
	// Checked here, not in sync: the hourly run would otherwise raise payment_error every hour.
	if !amountCovers(yp, p) {
		item.Note = "оплата не принята: сумма или валюта не совпадают с заказом"
		rep.Problems = append(rep.Problems, item)
		return
	}
	if err := s.syncProviderPayment(ctx, yp); err != nil {
		s.Log.Warn("reconcile sync", "payment", p.ID, "err", err)
	}
	after, err := store.GetPayment(ctx, s.Store.Pool, p.ID)
	switch {
	case err == nil && after.Status == "succeeded" && after.AppliedAt != nil:
		item.Note = "оплата была не обработана — покупка выдана"
		rep.Recovered = append(rep.Recovered, item)
		s.eventByID(ctx, "payment_recovered", p.CustomerID, map[string]any{"amount": rub(item.Amount),
			"payment": p.ID.String(), "plan": p.Title})
	case err == nil && after.Status == "succeeded":
		item.Note = "оплата отмечена, но покупка не применилась — повторю автоматически"
		rep.Problems = append(rep.Problems, item)
	default:
		item.Note = "оплата не принята — смотрите журнал сервера"
		rep.Problems = append(rep.Problems, item)
	}
}

// amountCovers: the provider took at least the order amount, in rubles.
func amountCovers(yp *yookassa.Payment, p *store.Payment) bool {
	return yp.Amount.Currency == "RUB" && yp.Amount.ToKopecks() >= p.Amount
}

// localPayment finds the order a YooKassa payment belongs to: by its id, or by our id in metadata
// (a retry without card saving creates a second YooKassa payment for the same order).
func (s *Service) localPayment(ctx context.Context, yp *yookassa.Payment) (*store.Payment, error) {
	p, err := store.GetPaymentByProviderRef(ctx, s.Store.Pool, "yookassa", yp.ID)
	if errors.Is(err, store.ErrNotFound) {
		if id, perr := uuid.Parse(yp.Metadata["payment_id"]); perr == nil {
			return store.GetPayment(ctx, s.Store.Pool, id)
		}
	}
	return p, err
}

// AutoReconcile runs the reconciliation over the last two days; recovered payments raise an admin event.
func (s *Service) AutoReconcile(ctx context.Context) {
	if _, err := s.gateway(); err != nil {
		return
	}
	now := time.Now()
	rep, err := s.ReconcileYooKassa(ctx, now.Add(-48*time.Hour), now)
	if err != nil {
		s.Log.Warn("auto reconcile", "err", err)
		return
	}
	if len(rep.Recovered) > 0 || len(rep.Problems) > 0 {
		s.Log.Info("auto reconcile", "checked", rep.Checked, "recovered", len(rep.Recovered), "problems", len(rep.Problems))
	}
}

// RecheckPayment asks YooKassa about one unapplied order, also a locally canceled one (admin "check").
func (s *Service) RecheckPayment(ctx context.Context, id uuid.UUID) (*store.Payment, error) {
	p, err := store.GetPayment(ctx, s.Store.Pool, id)
	if err != nil {
		return nil, userErr("платёж не найден")
	}
	if p.Provider != "yookassa" || p.ProviderRef == nil || p.AppliedAt != nil || p.Status == "refunded" {
		return p, nil
	}
	gw, err := s.gateway()
	if err != nil {
		return nil, err
	}
	yp, err := gw.GetPayment(ctx, *p.ProviderRef)
	if err != nil {
		return nil, errors.New(s.Redact("ЮKassa: " + err.Error()))
	}
	if err := s.syncProviderPayment(ctx, yp); err != nil {
		s.Log.Warn("recheck payment", "payment", p.ID, "err", err)
	}
	return store.GetPayment(ctx, s.Store.Pool, id)
}
