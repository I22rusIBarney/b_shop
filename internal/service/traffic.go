package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"bshop/internal/remnawave"
	"bshop/internal/store"
)

// TrafficOffer is what the customer can do with the traffic of a limited subscription.
type TrafficOffer struct {
	Topup       bool                   `json:"topup"`
	Packages    []store.TrafficPackage `json:"packages"`
	Reset       bool                   `json:"reset"`       // plan allows resetting
	CanReset    bool                   `json:"can_reset"`   // used enough to reset now
	ResetPrice  int64                  `json:"reset_price"` // kopecks, 0 = free
	Threshold   int                    `json:"threshold"`   // % used before reset is offered
	LimitBytes  int64                  `json:"limit_bytes"`
	ExtraBytes  int64                  `json:"extra_bytes"`
	UsedBytes   int64                  `json:"used_bytes"`
	Unavailable string                 `json:"unavailable,omitempty"`
	// Until is when traffic bought now (or already bought) ends: the next reset of the plan.
	Until      time.Time  `json:"until"`
	ExtraUntil *time.Time `json:"extra_until,omitempty"`
}

func (s *Service) trafficSub(ctx context.Context, c *store.Customer, subID int64) (*store.Subscription, *store.Plan, error) {
	sub, err := s.ownedSub(ctx, c, subID)
	if err != nil {
		return nil, nil, err
	}
	if !sub.Active() {
		return nil, nil, userErr("подписка неактивна — сначала продлите её")
	}
	if sub.PlanID == nil {
		return nil, nil, userErr("у подписки нет тарифа")
	}
	plan, err := store.GetPlan(ctx, s.Store.Pool, *sub.PlanID)
	if err != nil {
		return nil, nil, userErr("тариф подписки не найден")
	}
	if plan.TrafficLimitGB <= 0 {
		return nil, nil, userErr("у этого тарифа безлимитный трафик")
	}
	return sub, plan, nil
}

func (s *Service) TrafficOptions(ctx context.Context, c *store.Customer, subID int64) (*TrafficOffer, error) {
	sub, plan, err := s.trafficSub(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	o := &TrafficOffer{Topup: plan.TrafficTopupEnabled && len(plan.TrafficPackages) > 0, Packages: plan.TrafficPackages,
		Reset: plan.TrafficResetEnabled, ResetPrice: plan.TrafficResetPrice, Threshold: s.set().General.TrafficResetThreshold,
		LimitBytes: sub.TrafficLimitBytes, ExtraBytes: sub.ActiveExtraTraffic(time.Now()),
		Until: nextTrafficReset(plan.TrafficResetStrategy, time.Now(), sub.CreatedAt, sub.ExpiresAt)}
	if o.ExtraBytes > 0 {
		o.ExtraUntil = sub.ExtraTrafficUntil
	}
	if !o.Topup {
		o.Packages = []store.TrafficPackage{}
	}
	if u, err := s.Panel.GetUser(ctx, *sub.RWUserID); err == nil {
		o.UsedBytes = u.UserTraffic.UsedTrafficBytes
		o.LimitBytes = u.TrafficLimitBytes
	}
	o.CanReset = o.Reset && o.LimitBytes > 0 && o.UsedBytes*100 >= o.LimitBytes*int64(o.Threshold)
	return o, nil
}

// BuyTraffic starts a payment for one of the plan's extra-traffic packages.
func (s *Service) BuyTraffic(ctx context.Context, c *store.Customer, subID int64, pkg int, method, returnURL string) (*BuyResult, error) {
	sub, plan, err := s.trafficSub(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	if !plan.TrafficTopupEnabled || pkg < 0 || pkg >= len(plan.TrafficPackages) {
		return nil, userErr("докупка трафика для этого тарифа недоступна")
	}
	pk := plan.TrafficPackages[pkg]
	p := &store.Payment{CustomerID: c.ID, SubscriptionID: &sub.ID, PlanID: &plan.ID, Operation: "traffic",
		Title: plan.Name, TrafficGB: pk.GB, Amount: pk.Price, BaseAmount: pk.Price, Provider: "yookassa", Method: method}
	return s.startPayment(ctx, c, p, returnURL)
}

// ResetTrafficPaid resets used traffic now (free) or after payment (operation traffic_reset).
func (s *Service) ResetTrafficPaid(ctx context.Context, c *store.Customer, subID int64, method, returnURL string) (*BuyResult, error) {
	o, err := s.TrafficOptions(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	if !o.Reset {
		return nil, userErr("сброс трафика для этого тарифа недоступен")
	}
	if !o.CanReset {
		return nil, userErr(fmt.Sprintf("сброс доступен, когда израсходовано %d%% трафика", o.Threshold))
	}
	sub, plan, err := s.trafficSub(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	p := &store.Payment{CustomerID: c.ID, SubscriptionID: &sub.ID, PlanID: &plan.ID, Operation: "traffic_reset",
		Title: plan.Name, Amount: plan.TrafficResetPrice, BaseAmount: plan.TrafficResetPrice, Provider: "yookassa", Method: method}
	// Free resets go through the same path (provider "free") so they are logged like any other operation.
	return s.startPayment(ctx, c, p, returnURL)
}

// applyTraffic runs inside ApplyPayment for operations traffic and traffic_reset.
func (s *Service) applyTraffic(ctx context.Context, tx pgx.Tx, pay *store.Payment, plan *store.Plan) (*store.Subscription, error) {
	if pay.SubscriptionID == nil {
		return nil, errors.New("traffic payment without subscription")
	}
	sub, err := store.GetSubscriptionForUpdate(ctx, tx, *pay.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if sub.RWUserID == nil {
		return nil, errors.New("subscription has no panel user")
	}
	switch pay.Operation {
	case "traffic":
		now := time.Now()
		until := nextTrafficReset(plan.TrafficResetStrategy, now, sub.CreatedAt, sub.ExpiresAt)
		sub.ExtraTrafficBytes = sub.ActiveExtraTraffic(now) + int64(pay.TrafficGB)<<30
		sub.ExtraTrafficUntil = &until
		limit := plan.TrafficBytes() + sub.ExtraTrafficBytes
		u, err := s.Panel.UpdateUser(ctx, remnawave.UpdateUser{ID: *sub.RWUserID, TrafficLimitBytes: &limit})
		if err != nil {
			return nil, fmt.Errorf("панель: %w", err)
		}
		sub.TrafficLimitBytes = u.TrafficLimitBytes
	case "traffic_reset":
		if _, err := s.Panel.ResetTraffic(ctx, *sub.RWUserID); err != nil {
			return nil, fmt.Errorf("панель: %w", err)
		}
	}
	return sub, store.UpdateSubscription(ctx, tx, sub)
}

// nextTrafficReset is when the panel next zeroes used traffic for the strategy (UTC calendar boundaries);
// without resets it is the end of the paid period.
func nextTrafficReset(strategy string, now, anchor, expires time.Time) time.Time {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch rwStrategy(strategy) {
	case "DAY":
		return day.AddDate(0, 0, 1)
	case "WEEK":
		d := (8 - int(day.Weekday())) % 7 // days to next Monday
		if d == 0 {
			d = 7
		}
		return day.AddDate(0, 0, d)
	case "MONTH_ROLLING":
		a := anchor.UTC().Day()
		for m := 0; m < 3; m++ {
			y, mo, _ := now.AddDate(0, m, 0).Date()
			last := time.Date(y, mo+1, 0, 0, 0, 0, 0, time.UTC).Day()
			t := time.Date(y, mo, min(a, last), 0, 0, 0, 0, time.UTC)
			if t.After(now) {
				return t
			}
		}
		return day.AddDate(0, 1, 0)
	case "NO_RESET":
		return expires
	default: // MONTH
		return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
}

// topupGrace: if the panel has not reported its own reset by then, the bought traffic is removed anyway.
const topupGrace = 6 * time.Hour

// EndTrafficTopups removes bought traffic whose period has ended. With periodic resets it waits until the panel
// has actually reset used traffic, so the lower limit never cuts off a customer who used the package.
func (s *Service) EndTrafficTopups(ctx context.Context) {
	subs, err := store.SubsWithEndedTopup(ctx, s.Store.Pool)
	if err != nil {
		s.Log.Warn("traffic topups", "err", err)
		return
	}
	for i := range subs {
		sb := &subs[i]
		if err := s.endTopup(ctx, sb.ID); err != nil {
			s.Log.Warn("end traffic topup", "sub", sb.ID, "err", err)
		}
	}
}

func (s *Service) endTopup(ctx context.Context, id int64) error {
	return s.Store.Tx(ctx, func(tx pgx.Tx) error {
		sb, err := store.GetSubscriptionForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		// A package bought meanwhile moved the end time forward: nothing to do yet.
		if sb.ExtraTrafficBytes <= 0 || sb.ExtraTrafficUntil == nil || time.Now().Before(*sb.ExtraTrafficUntil) {
			return nil
		}
		var plan *store.Plan
		if sb.PlanID != nil {
			plan, _ = store.GetPlan(ctx, tx, *sb.PlanID)
		}
		if sb.RWUserID != nil && plan != nil && plan.TrafficLimitGB > 0 {
			u, err := s.Panel.GetUser(ctx, *sb.RWUserID)
			if err != nil && !errors.Is(err, remnawave.ErrNotFound) {
				return err
			}
			if u != nil {
				periodic := rwStrategy(plan.TrafficResetStrategy) != "NO_RESET"
				resetDone := u.LastTrafficResetAt != nil && !u.LastTrafficResetAt.Before(sb.ExtraTrafficUntil.Add(-time.Hour))
				if periodic && !resetDone && time.Since(*sb.ExtraTrafficUntil) < topupGrace {
					return nil // wait for the panel's own reset
				}
				limit := plan.TrafficBytes()
				if _, err := s.Panel.UpdateUser(ctx, remnawave.UpdateUser{ID: *sb.RWUserID, TrafficLimitBytes: &limit}); err != nil {
					return err
				}
				sb.TrafficLimitBytes = limit
			}
		}
		sb.ExtraTrafficBytes = 0
		sb.ExtraTrafficUntil = nil
		return store.UpdateSubscription(ctx, tx, sb)
	})
}
