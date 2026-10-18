package service

import (
	"context"
	"errors"
	"fmt"

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
		LimitBytes: sub.TrafficLimitBytes, ExtraBytes: sub.ExtraTrafficBytes}
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
		sub.ExtraTrafficBytes += int64(pay.TrafficGB) << 30
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
