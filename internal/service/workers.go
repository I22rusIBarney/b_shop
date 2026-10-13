package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"bshop/internal/moynalog"
	"bshop/internal/store"
)

// receiptBackoff grows from 1 minute to 6 hours.
func receiptBackoff(attempt int) time.Duration {
	steps := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour}
	if attempt >= len(steps) {
		return steps[len(steps)-1]
	}
	return steps[attempt]
}

// ReceiptName renders the Moy Nalog service name for a payment.
func (s *Service) ReceiptName(p *store.Payment) string {
	op := map[string]string{"buy": "покупка", "renew": "продление", "autopay": "продление", "change": "смена тарифа",
		"devices": "доп. устройства", "traffic": "доп. трафик"}[p.Operation]
	months := p.Months
	if months == 0 && p.Days > 0 {
		months = 1
	}
	name := strings.NewReplacer("{plan}", p.Title, "{months}", strconv.Itoa(months), "{days}", strconv.Itoa(p.Days),
		"{operation}", op).Replace(s.set().MoyNalog.Template)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Доступ к VPN-сервису " + p.Title
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return name
}

// ProcessReceipts issues and annuls due Moy Nalog receipts.
func (s *Service) ProcessReceipts(ctx context.Context) {
	r := s.receipts()
	if r == nil {
		return
	}
	due, err := store.ReceiptsDue(ctx, s.Store.Pool, 20)
	if err != nil {
		s.Log.Error("receipts due", "err", err)
		return
	}
	for i := range due {
		p := &due[i]
		next := time.Now().Add(receiptBackoff(p.ReceiptAttempts))
		if p.ReceiptStatus == "cancel_pending" {
			if err := r.CancelIncome(ctx, p.ReceiptUUID, "Возврат средств"); err != nil {
				_ = store.ReceiptRetry(ctx, s.Store.Pool, p.ID, "cancel_pending", err.Error(), next)
				s.receiptAlert(ctx, p, err)
				continue
			}
			_ = store.ReceiptCanceled(ctx, s.Store.Pool, p.ID)
			continue
		}
		if p.Status == "refunded" {
			_ = store.ReceiptCanceled(ctx, s.Store.Pool, p.ID)
			continue
		}
		at := p.CreatedAt
		if p.PaidAt != nil {
			at = *p.PaidAt
		}
		uuid, err := r.CreateIncome(ctx, s.ReceiptName(p), p.Amount, at)
		if err != nil {
			_ = store.ReceiptRetry(ctx, s.Store.Pool, p.ID, "failed", err.Error(), next)
			s.receiptAlert(ctx, p, err)
			continue
		}
		_ = store.ReceiptDone(ctx, s.Store.Pool, p.ID, uuid)
	}
}

func (s *Service) receiptAlert(ctx context.Context, p *store.Payment, err error) {
	// Alert on the first failure and when auth is broken; later retries stay quiet.
	if p.ReceiptAttempts == 0 || (errors.Is(err, moynalog.ErrAuth) && p.ReceiptAttempts%5 == 0) {
		s.eventByID(ctx, "receipt_error", p.CustomerID, map[string]any{"payment": p.ID.String(), "amount": rub(p.Amount), "error": err.Error()})
	}
}

// ReconcilePayments re-checks pending payments, expires stale ones and retries failed provisioning.
func (s *Service) ReconcilePayments(ctx context.Context) {
	if gw, err := s.gateway(); err == nil {
		pending, err := store.PendingPaymentsToCheck(ctx, s.Store.Pool)
		if err == nil {
			for _, p := range pending {
				yp, err := gw.GetPayment(ctx, *p.ProviderRef)
				if err != nil {
					continue
				}
				if err := s.syncProviderPayment(ctx, yp); err != nil {
					s.Log.Warn("sync payment", "payment", p.ID, "err", err)
				}
			}
		}
	}
	minutes := s.set().Payments.PendingMinutes
	if _, err := store.ExpireStalePayments(ctx, s.Store.Pool, time.Duration(minutes)*time.Minute); err != nil {
		s.Log.Error("expire payments", "err", err)
	}
	stuck, err := store.PaidNotApplied(ctx, s.Store.Pool)
	if err != nil {
		return
	}
	for _, p := range stuck {
		_ = s.ApplyPayment(ctx, p.ID)
	}
	refs, err := store.ReferralPending(ctx, s.Store.Pool)
	if err != nil {
		return
	}
	for _, p := range refs {
		if err := s.ProcessReferral(ctx, p.ID); err != nil {
			s.Log.Warn("referral retry", "payment", p.ID, "err", err)
		}
	}
}

// RunWorkers starts periodic background jobs until ctx is done.
func (s *Service) RunWorkers(ctx context.Context) {
	every := func(d time.Duration, name string, fn func(context.Context)) {
		go func() {
			t := time.NewTicker(d)
			defer t.Stop()
			for {
				func() {
					defer func() {
						if r := recover(); r != nil {
							s.Log.Error("worker panic", "worker", name, "panic", r)
						}
					}()
					jctx, cancel := context.WithTimeout(ctx, d*3)
					defer cancel()
					fn(jctx)
				}()
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	every(time.Minute, "payments", s.ReconcilePayments)
	every(30*time.Second, "receipts", s.ProcessReceipts)
	every(10*time.Minute, "autopay", s.RunAutopay)
	every(time.Hour, "purge", func(ctx context.Context) {
		_ = store.PurgeExpired(ctx, s.Store.Pool)
		s.CleanWebhookLog(ctx)
	})
	every(10*time.Second, "ai", s.ProcessAI)
	every(15*time.Second, "reply-notices", s.ProcessReplyNotices)
	every(5*time.Minute, "ticket-timers", s.ProcessTicketTimers)
	every(time.Minute, "broadcasts", s.ProcessBroadcasts)
	every(5*time.Minute, "automation-triggers", s.ScanAutomationTriggers)
	every(time.Minute, "automation-runs", s.ProcessAutomationRuns)
}
