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

// ActivateTrial gives a first-time customer the configured trial.
func (s *Service) ActivateTrial(ctx context.Context, c *store.Customer) (*store.Subscription, error) {
	t := s.set().Trial
	if !t.Enabled {
		return nil, userErr("пробный период сейчас недоступен")
	}
	unlock := s.lock(fmt.Sprintf("trial:%d", c.ID))
	defer unlock()
	var sub *store.Subscription
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		cur, err := store.GetCustomerForUpdate(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if cur.TrialUsed {
			return userErr("пробный период уже был использован")
		}
		if n, err := store.CountSubscriptions(ctx, tx, c.ID, nil); err != nil {
			return err
		} else if n > 0 {
			return userErr("пробный период доступен только новым клиентам")
		}
		plan, err := store.GetPlan(ctx, tx, t.PlanID)
		if err != nil {
			return userErr("пробный период настроен неверно, напишите в поддержку")
		}
		sub, err = s.provisionNew(ctx, tx, cur, Grant{Plan: plan, Kind: "trial", Days: t.Days,
			Username: fmt.Sprintf("u%d_trial", cur.ID)})
		if err != nil {
			return err
		}
		if err := store.SetTrialUsed(ctx, tx, cur.ID); err != nil {
			return err
		}
		return store.AddEvent(ctx, tx, cur.ID, "trial", map[string]any{"days": t.Days, "subscription": sub.ID}, nil)
	})
	if err != nil {
		var ue *UserError
		if !errors.As(err, &ue) {
			s.Log.Error("trial", "customer", c.ID, "err", err)
			return nil, userErr("не удалось активировать пробный период, попробуйте позже")
		}
		return nil, err
	}
	s.Notify.Admin(ctx, "trial", fmt.Sprintf("⭐ Активирован триал\n👤 %s · %d дн.", escape(c.DisplayName()), t.Days), c.ID)
	return sub, nil
}

// SubscriptionView is a subscription enriched with live panel data.
type SubscriptionView struct {
	store.Subscription
	PlanName         string     `json:"plan_name"`
	UsedTrafficBytes int64      `json:"used_traffic_bytes"`
	OnlineAt         *time.Time `json:"online_at"`
	FirstConnectedAt *time.Time `json:"first_connected_at"`
	DevicesUsed      int        `json:"devices_used"`
	PanelStatus      string     `json:"panel_status"`
	CanRenew         bool       `json:"can_renew"`
	CanChangePlan    bool       `json:"can_change_plan"`
	CanResetTraffic  bool       `json:"can_reset_traffic"`
}

func (s *Service) Subscriptions(ctx context.Context, c *store.Customer, live bool) ([]SubscriptionView, error) {
	subs, err := store.ListSubscriptions(ctx, s.Store.Pool, c.ID)
	if err != nil {
		return nil, err
	}
	plans, err := store.ListPlans(ctx, s.Store.Pool)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*store.Plan{}
	for i := range plans {
		byID[plans[i].ID] = &plans[i]
	}
	threshold := s.set().General.TrafficResetThreshold
	out := make([]SubscriptionView, 0, len(subs))
	for _, sb := range subs {
		v := SubscriptionView{Subscription: sb}
		if sb.PlanID != nil {
			if p := byID[*sb.PlanID]; p != nil {
				v.PlanName = p.Name
				v.CanRenew = !p.RenewalDisabled && p.IsActive && sb.Kind != "trial"
				v.CanChangePlan = !p.ChangeFromDisabled && !(sb.Kind == "trial" && s.set().Trial.HidePlanChange)
			}
		}
		if live && sb.RWUserID != nil {
			if u, err := s.Panel.GetUser(ctx, *sb.RWUserID); err == nil {
				v.UsedTrafficBytes = u.UserTraffic.UsedTrafficBytes
				v.OnlineAt = u.UserTraffic.OnlineAt
				v.FirstConnectedAt = u.UserTraffic.FirstConnectedAt
				v.PanelStatus = u.Status
				if u.SubscriptionURL != "" {
					v.SubscriptionURL = u.SubscriptionURL
				}
			}
			if devs, err := s.Panel.UserDevices(ctx, *sb.RWUserID); err == nil {
				v.DevicesUsed = len(devs)
			}
		}
		if sb.TrafficLimitBytes > 0 && threshold > 0 {
			v.CanResetTraffic = v.UsedTrafficBytes*100 >= sb.TrafficLimitBytes*int64(threshold)
		}
		out = append(out, v)
	}
	return out, nil
}

// ownedSub loads a subscription and checks it belongs to the customer (admins may act on any).
func (s *Service) ownedSub(ctx context.Context, c *store.Customer, id int64) (*store.Subscription, error) {
	sub, err := store.GetSubscription(ctx, s.Store.Pool, id)
	if err != nil || (sub.CustomerID != c.ID && !c.IsAdmin) {
		return nil, userErr("подписка не найдена")
	}
	if sub.RWUserID == nil {
		return nil, userErr("подписка ещё не выдана в панели")
	}
	return sub, nil
}

func (s *Service) Devices(ctx context.Context, c *store.Customer, subID int64) ([]remnawave.Device, error) {
	sub, err := s.ownedSub(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	devs, err := s.Panel.UserDevices(ctx, *sub.RWUserID)
	if devs == nil {
		devs = []remnawave.Device{}
	}
	return devs, err
}

func (s *Service) DeleteDevice(ctx context.Context, c *store.Customer, subID int64, hwid string) error {
	sub, err := s.ownedSub(ctx, c, subID)
	if err != nil {
		return err
	}
	if hwid == "" {
		err = s.Panel.DeleteAllDevices(ctx, *sub.RWUserID)
	} else {
		err = s.Panel.DeleteDevice(ctx, *sub.RWUserID, hwid)
	}
	if err == nil {
		_ = store.AddEvent(ctx, s.Store.Pool, sub.CustomerID, "device_deleted", map[string]any{"subscription": sub.ID, "all": hwid == ""}, &c.ID)
	}
	return err
}

// RevokeLink issues a new subscription link (and passwords); onlyPasswords keeps the link.
func (s *Service) RevokeLink(ctx context.Context, c *store.Customer, subID int64, onlyPasswords bool) (*store.Subscription, error) {
	sub, err := s.ownedSub(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	u, err := s.Panel.RevokeSubscription(ctx, *sub.RWUserID, onlyPasswords)
	if err != nil {
		return nil, err
	}
	short := u.ShortUUID
	sub.RWShortUUID = &short
	sub.SubscriptionURL = u.SubscriptionURL
	if err := store.UpdateSubscription(ctx, s.Store.Pool, sub); err != nil {
		return nil, err
	}
	_ = store.AddEvent(ctx, s.Store.Pool, sub.CustomerID, "subscription_revoked", map[string]any{"subscription": sub.ID, "only_passwords": onlyPasswords}, &c.ID)
	return sub, nil
}

func (s *Service) SetPrimary(ctx context.Context, c *store.Customer, subID int64) error {
	sub, err := store.GetSubscription(ctx, s.Store.Pool, subID)
	if err != nil || sub.CustomerID != c.ID {
		return userErr("подписка не найдена")
	}
	return store.SetPrimarySubscription(ctx, s.Store.Pool, c.ID, subID)
}
