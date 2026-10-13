package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bshop/internal/remnawave"
	"bshop/internal/store"

	"github.com/jackc/pgx/v5"
)

// AdminExtend adds (or with negative days removes) days on a subscription.
func (s *Service) AdminExtend(ctx context.Context, admin *store.Customer, subID int64, days int) (*store.Subscription, error) {
	if days == 0 || days > 3650 || days < -3650 {
		return nil, userErr("укажите число дней")
	}
	var sub *store.Subscription
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		sub, err = store.GetSubscriptionForUpdate(ctx, tx, subID)
		if err != nil {
			return err
		}
		if err := s.addDays(ctx, tx, sub, days); err != nil {
			return err
		}
		return store.AddEvent(ctx, tx, sub.CustomerID, "admin_days", map[string]any{"subscription": sub.ID, "days": days}, &admin.ID)
	})
	return sub, err
}

// addDays moves a subscription's expiry by days (negative removes) in the panel and the DB.
// It keeps the plan; an expired subscription is extended from now.
func (s *Service) addDays(ctx context.Context, db store.DB, sub *store.Subscription, days int) error {
	base := sub.ExpiresAt
	if days > 0 && base.Before(time.Now()) {
		base = time.Now().UTC()
	}
	expire := base.Add(time.Duration(days) * 24 * time.Hour)
	if !expire.After(time.Now()) {
		expire = time.Now().Add(time.Minute)
	}
	if sub.RWUserID != nil {
		u, err := s.Panel.UpdateUser(ctx, remnawave.UpdateUser{ID: *sub.RWUserID, Status: "ACTIVE", ExpireAt: &expire})
		if err != nil {
			return fmt.Errorf("панель: %w", err)
		}
		sub.ExpiresAt = u.ExpireAt
	} else {
		sub.ExpiresAt = expire
	}
	sub.Status = "active"
	return store.UpdateSubscription(ctx, db, sub)
}

// AdminGrant creates a subscription on a plan without payment.
func (s *Service) AdminGrant(ctx context.Context, admin *store.Customer, customerID, planID int64, days int, kind string) (*store.Subscription, error) {
	if days < 1 || days > 3650 {
		return nil, userErr("укажите срок от 1 дня")
	}
	if kind != "trial" && kind != "free" && kind != "paid" {
		kind = "free"
	}
	var sub *store.Subscription
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		c, err := store.GetCustomerForUpdate(ctx, tx, customerID)
		if err != nil {
			return err
		}
		plan, err := store.GetPlan(ctx, tx, planID)
		if err != nil {
			return userErr("тариф не найден")
		}
		sub, err = s.provisionNew(ctx, tx, c, Grant{Plan: plan, Kind: kind, Days: days,
			Username: fmt.Sprintf("u%d_a%d", c.ID, time.Now().Unix())})
		if err != nil {
			return err
		}
		if kind == "trial" {
			_ = store.SetTrialUsed(ctx, tx, c.ID)
		}
		return store.AddEvent(ctx, tx, c.ID, "admin_grant", map[string]any{"subscription": sub.ID, "plan": plan.Name, "days": days, "kind": kind}, &admin.ID)
	})
	return sub, err
}

func (s *Service) AdminResetTraffic(ctx context.Context, admin *store.Customer, subID int64) error {
	sub, err := s.ownedSub(ctx, admin, subID)
	if err != nil {
		return err
	}
	if _, err := s.Panel.ResetTraffic(ctx, *sub.RWUserID); err != nil {
		return err
	}
	return store.AddEvent(ctx, s.Store.Pool, sub.CustomerID, "traffic_reset", map[string]any{"subscription": sub.ID}, &admin.ID)
}

// AdminDeleteSubscription removes the subscription and its panel user.
func (s *Service) AdminDeleteSubscription(ctx context.Context, admin *store.Customer, subID int64) error {
	sub, err := store.GetSubscription(ctx, s.Store.Pool, subID)
	if err != nil {
		return err
	}
	if sub.RWUserID != nil {
		if err := s.Panel.DeleteUser(ctx, *sub.RWUserID); err != nil && !errors.Is(err, remnawave.ErrNotFound) {
			return err
		}
	}
	if err := store.DeleteSubscription(ctx, s.Store.Pool, subID); err != nil {
		return err
	}
	return store.AddEvent(ctx, s.Store.Pool, sub.CustomerID, "subscription_deleted", map[string]any{"subscription": sub.ID, "title": sub.Title}, &admin.ID)
}

// AdminSetStatus enables or disables a subscription in the panel.
func (s *Service) AdminSetStatus(ctx context.Context, admin *store.Customer, subID int64, enabled bool) error {
	sub, err := s.ownedSub(ctx, admin, subID)
	if err != nil {
		return err
	}
	if enabled {
		_, err = s.Panel.EnableUser(ctx, *sub.RWUserID)
		sub.Status = "active"
	} else {
		_, err = s.Panel.DisableUser(ctx, *sub.RWUserID)
		sub.Status = "disabled"
	}
	if err != nil {
		return err
	}
	if err := store.UpdateSubscription(ctx, s.Store.Pool, sub); err != nil {
		return err
	}
	return store.AddEvent(ctx, s.Store.Pool, sub.CustomerID, "subscription_status", map[string]any{"subscription": sub.ID, "enabled": enabled}, &admin.ID)
}

type Event struct {
	ID        int64          `json:"id"`
	Kind      string         `json:"kind"`
	Data      map[string]any `json:"data"`
	ActorID   *int64         `json:"actor_id"`
	CreatedAt time.Time      `json:"created_at"`
}

func (s *Service) CustomerEvents(ctx context.Context, customerID int64, limit int) ([]Event, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT id, kind, data, actor_id, created_at FROM customer_events
		WHERE customer_id=$1 ORDER BY id DESC LIMIT $2`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.Data, &e.ActorID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
