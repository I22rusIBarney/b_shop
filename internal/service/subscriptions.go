package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	s.event(ctx, "trial", c, map[string]any{"days": t.Days, "plan": sub.Title,
		"expires": sub.ExpiresAt.In(s.set().General.Location()).Format("2006-01-02 15:04")})
	s.AutomationEvent(ctx, "active_trial", c.ID, map[string]any{"plan_name": sub.Title, "subscription_id": sub.ID})
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
	// What the plan allows for traffic (only plans with a limit): buying packages, resetting used traffic.
	TrafficTopup bool `json:"traffic_topup"`
	TrafficReset bool `json:"traffic_reset"`
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
				v.TrafficTopup = p.TrafficLimitGB > 0 && p.TrafficTopupEnabled && len(p.TrafficPackages) > 0
				v.TrafficReset = p.TrafficLimitGB > 0 && p.TrafficResetEnabled
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
		if v.TrafficReset && sb.TrafficLimitBytes > 0 {
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

// DeviceView is a panel HWID device plus what the cabinet shows on top of it.
type DeviceView struct {
	remnawave.Device
	Name string `json:"name"` // custom name set by the client, empty if none
	App  string `json:"app"`  // client app parsed from the User-Agent (Happ, INCY, v2rayNG…)
}

// AppFromUserAgent extracts the client app name: "Happ/3.7.0/ios" → "Happ".
func AppFromUserAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return ""
	}
	name := ua
	if i := strings.IndexAny(name, "/ ("); i > 0 {
		name = name[:i]
	}
	if len(name) > 24 {
		name = name[:24]
	}
	return name
}

func (s *Service) Devices(ctx context.Context, c *store.Customer, subID int64) ([]DeviceView, error) {
	sub, err := s.ownedSub(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	devs, err := s.Panel.UserDevices(ctx, *sub.RWUserID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	rows, err := s.Store.Pool.Query(ctx, `SELECT hwid, name FROM device_names WHERE subscription_id = $1`, sub.ID)
	if err == nil {
		for rows.Next() {
			var h, n string
			if rows.Scan(&h, &n) == nil {
				names[h] = n
			}
		}
		rows.Close()
	}
	out := make([]DeviceView, 0, len(devs))
	for _, d := range devs {
		v := DeviceView{Device: d, Name: names[d.HWID]}
		if d.UserAgent != nil {
			v.App = AppFromUserAgent(*d.UserAgent)
		}
		out = append(out, v)
	}
	return out, nil
}

// RenameDevice stores a custom device name; an empty name resets it.
func (s *Service) RenameDevice(ctx context.Context, c *store.Customer, subID int64, hwid, name string) error {
	sub, err := s.ownedSub(ctx, c, subID)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if hwid == "" {
		return userErr("не указано устройство")
	}
	if len([]rune(name)) > 40 {
		return userErr("название длиннее 40 символов")
	}
	if name == "" {
		_, err = s.Store.Pool.Exec(ctx, `DELETE FROM device_names WHERE subscription_id = $1 AND hwid = $2`, sub.ID, hwid)
		return err
	}
	_, err = s.Store.Pool.Exec(ctx, `INSERT INTO device_names (subscription_id, hwid, name) VALUES ($1, $2, $3)
		ON CONFLICT (subscription_id, hwid) DO UPDATE SET name = EXCLUDED.name, updated_at = now()`, sub.ID, hwid, name)
	return err
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
		if hwid == "" {
			_, _ = s.Store.Pool.Exec(ctx, `DELETE FROM device_names WHERE subscription_id = $1`, sub.ID)
		} else {
			_, _ = s.Store.Pool.Exec(ctx, `DELETE FROM device_names WHERE subscription_id = $1 AND hwid = $2`, sub.ID, hwid)
		}
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
