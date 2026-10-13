package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bshop/internal/remnawave"
	"bshop/internal/store"
)

// Grant describes what a subscription should look like in the panel after an operation.
type Grant struct {
	Plan         *store.Plan
	Kind         string // trial | paid | free
	Days         int    // days to add (renew) or initial length (new)
	ExtraDevices int    // extra device slots to add
	// Username is deterministic per operation so a retried provisioning finds the user it already created.
	Username string
}

func rwStrategy(s string) string {
	switch s {
	case "NO_RESET", "DAY", "WEEK", "MONTH", "MONTH_ROLLING":
		return s
	}
	return "MONTH"
}

func newExpire(current time.Time, days int) time.Time {
	now := time.Now().UTC()
	if current.Before(now) {
		current = now
	}
	return current.Add(time.Duration(days) * 24 * time.Hour)
}

func (s *Service) panelDescription(c *store.Customer) string {
	d := fmt.Sprintf("bshop #%d", c.ID)
	if c.Username != nil && *c.Username != "" {
		d += " @" + *c.Username
	} else if c.Email != nil {
		d += " " + *c.Email
	}
	return d
}

// createPanelUser creates (or finds, on retry) the panel user for a new subscription.
func (s *Service) createPanelUser(ctx context.Context, c *store.Customer, g Grant, expire time.Time, devices int) (*remnawave.User, error) {
	if u, err := s.Panel.GetUserByUsername(ctx, g.Username); err == nil {
		return u, nil
	} else if !errors.Is(err, remnawave.ErrNotFound) {
		return nil, err
	}
	in := remnawave.CreateUser{
		Username:             g.Username,
		Status:               "ACTIVE",
		ExpireAt:             expire,
		TrafficLimitBytes:    g.Plan.TrafficBytes(),
		TrafficLimitStrategy: rwStrategy(g.Plan.TrafficResetStrategy),
		Description:          s.panelDescription(c),
		TelegramID:           c.TelegramID,
		Email:                c.Email,
		ActiveInternalSquads: g.Plan.RWInternalSquads,
		ExternalSquadUUID:    g.Plan.RWExternalSquad,
	}
	if g.Plan.RWTag != "" {
		t := g.Plan.RWTag
		in.Tag = &t
	}
	if devices > 0 {
		in.HwidDeviceLimit = &devices
	}
	return s.Panel.CreateUser(ctx, in)
}

// applyPlanToPanel pushes plan parameters, expiry and device limit to an existing panel user.
func (s *Service) applyPlanToPanel(ctx context.Context, sub *store.Subscription, plan *store.Plan, expire time.Time, resetTraffic bool) (*remnawave.User, error) {
	devices := sub.TotalDevices()
	traffic := plan.TrafficBytes()
	up := remnawave.UpdateUser{
		ID:                   *sub.RWUserID,
		Status:               "ACTIVE",
		ExpireAt:             &expire,
		TrafficLimitBytes:    &traffic,
		TrafficLimitStrategy: rwStrategy(plan.TrafficResetStrategy),
		HwidDeviceLimit:      &devices,
		ActiveInternalSquads: plan.RWInternalSquads,
	}
	if up.ActiveInternalSquads == nil {
		up.ActiveInternalSquads = nil
	}
	if plan.RWTag != "" {
		t := plan.RWTag
		up.Tag = &t
	} else {
		up.ClearTag = true
	}
	if plan.RWExternalSquad != nil {
		up.ExternalSquadUUID = plan.RWExternalSquad
	} else {
		up.ClearExternalSquad = true
	}
	u, err := s.Panel.UpdateUser(ctx, up)
	if err != nil {
		return nil, err
	}
	if resetTraffic {
		if r, err := s.Panel.ResetTraffic(ctx, u.ID); err == nil {
			u = r
		} else {
			s.Log.Warn("reset traffic", "sub", sub.ID, "err", err)
		}
	}
	return u, nil
}

func fillFromPanel(sub *store.Subscription, u *remnawave.User) {
	id := u.ID
	sub.RWUserID = &id
	short := u.ShortUUID
	sub.RWShortUUID = &short
	name := u.Username
	sub.RWUsername = &name
	sub.SubscriptionURL = u.SubscriptionURL
	sub.ExpiresAt = u.ExpireAt
	sub.TrafficLimitBytes = u.TrafficLimitBytes
	sub.Status = "active"
}

// provisionNew creates a subscription for the customer.
func (s *Service) provisionNew(ctx context.Context, db store.DB, c *store.Customer, g Grant) (*store.Subscription, error) {
	expire := newExpire(time.Time{}, g.Days)
	devices := 0
	if g.Plan.DeviceLimit > 0 {
		devices = g.Plan.DeviceLimit + g.ExtraDevices
	}
	u, err := s.createPanelUser(ctx, c, g, expire, devices)
	if err != nil {
		return nil, fmt.Errorf("panel create: %w", err)
	}
	planID := g.Plan.ID
	sub := &store.Subscription{CustomerID: c.ID, PlanID: &planID, Kind: g.Kind, Title: g.Plan.Name,
		DeviceLimit: g.Plan.DeviceLimit, ExtraDevices: g.ExtraDevices}
	fillFromPanel(sub, u)
	if err := store.InsertSubscription(ctx, db, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// provisionExtend adds days to an existing subscription and re-applies its plan.
// If the panel user disappeared it is recreated.
func (s *Service) provisionExtend(ctx context.Context, db store.DB, c *store.Customer, sub *store.Subscription, g Grant) error {
	expire := newExpire(sub.ExpiresAt, g.Days)
	sub.ExtraDevices += g.ExtraDevices
	planID := g.Plan.ID
	sub.PlanID = &planID
	sub.Kind = g.Kind
	sub.Title = g.Plan.Name
	sub.DeviceLimit = g.Plan.DeviceLimit

	var u *remnawave.User
	var err error
	if sub.RWUserID != nil {
		u, err = s.applyPlanToPanel(ctx, sub, g.Plan, expire, g.Plan.ResetTrafficOnBuy)
		if errors.Is(err, remnawave.ErrNotFound) {
			sub.RWUserID = nil
		} else if err != nil {
			return fmt.Errorf("panel update: %w", err)
		}
	}
	if sub.RWUserID == nil {
		u, err = s.createPanelUser(ctx, c, g, expire, sub.TotalDevices())
		if err != nil {
			return fmt.Errorf("panel create: %w", err)
		}
	}
	fillFromPanel(sub, u)
	return store.UpdateSubscription(ctx, db, sub)
}
