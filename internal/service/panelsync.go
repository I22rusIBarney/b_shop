package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"bshop/internal/remnawave"
	"bshop/internal/store"
)

// Sync with the Remnawave panel ("Синхронизировать с панелью"): every panel user becomes a subscription
// in the shop. Known subscriptions are refreshed (expiry, traffic, link); unknown panel users are attached
// to the customer with the same Telegram ID or email, or a new customer is created for them.
// Users with neither Telegram ID nor email can't be matched to anyone and are skipped.

type PanelSyncState struct {
	Running    bool       `json:"running"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Total      int        `json:"total"`
	Done       int        `json:"done"`
	Updated    int        `json:"updated"`  // subscriptions refreshed
	Attached   int        `json:"attached"` // new subscription for an existing customer
	Created    int        `json:"created"`  // new customers
	Skipped    int        `json:"skipped"`  // no Telegram ID / email
	Errors     []string   `json:"errors"`
	Error      string     `json:"error"`
}

type panelSync struct {
	mu    sync.Mutex
	state PanelSyncState
}

func (s *Service) panelSyncJob() *panelSync {
	s.syncOnce.Do(func() { s.psync = &panelSync{state: PanelSyncState{Errors: []string{}}} })
	return s.psync
}

func (s *Service) PanelSyncStatus() PanelSyncState {
	j := s.panelSyncJob()
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.state
	st.Errors = append([]string{}, j.state.Errors...)
	return st
}

// StartPanelSync runs the sync in the background; the admin page polls PanelSyncStatus.
func (s *Service) StartPanelSync() (PanelSyncState, error) {
	j := s.panelSyncJob()
	j.mu.Lock()
	if j.state.Running {
		j.mu.Unlock()
		return PanelSyncState{}, userErr("синхронизация уже идёт")
	}
	now := time.Now()
	j.state = PanelSyncState{Running: true, StartedAt: &now, Errors: []string{}}
	j.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		err := s.SyncPanelUsers(ctx, func(f func(*PanelSyncState)) {
			j.mu.Lock()
			f(&j.state)
			j.mu.Unlock()
		})
		j.mu.Lock()
		end := time.Now()
		j.state.Running, j.state.FinishedAt = false, &end
		if err != nil {
			j.state.Error = err.Error()
		}
		st := j.state
		j.mu.Unlock()
		if err != nil {
			s.Log.Error("panel sync", "err", err)
			return
		}
		s.Log.Info("panel sync", "total", st.Total, "updated", st.Updated, "attached", st.Attached, "created", st.Created, "skipped", st.Skipped)
	}()
	return s.PanelSyncStatus(), nil
}

// SyncPanelUsers walks all panel users. update receives progress (may be a no-op).
func (s *Service) SyncPanelUsers(ctx context.Context, update func(func(*PanelSyncState))) error {
	plans, err := store.ListPlans(ctx, s.Store.Pool)
	if err != nil {
		return err
	}
	byTag := map[string]*store.Plan{}
	for i := range plans {
		if t := strings.TrimSpace(plans[i].RWTag); t != "" {
			byTag[strings.ToUpper(t)] = &plans[i]
		}
	}
	fail := func(u remnawave.User, err error) {
		update(func(st *PanelSyncState) {
			if len(st.Errors) < 50 {
				st.Errors = append(st.Errors, fmt.Sprintf("%s: %v", u.Username, err))
			}
		})
	}
	return s.Panel.ListUsers(ctx, func(users []remnawave.User) error {
		update(func(st *PanelSyncState) { st.Total += len(users) })
		for _, u := range users {
			if err := ctx.Err(); err != nil {
				return err
			}
			res, err := s.syncPanelUser(ctx, u, byTag)
			update(func(st *PanelSyncState) {
				st.Done++
				switch res {
				case "updated":
					st.Updated++
				case "attached":
					st.Attached++
				case "created":
					st.Created++
				case "skipped":
					st.Skipped++
				}
			})
			if err != nil {
				fail(u, err)
			}
		}
		return nil
	})
}

func (s *Service) syncPanelUser(ctx context.Context, u remnawave.User, byTag map[string]*store.Plan) (string, error) {
	if sub, err := store.GetSubscriptionByRWUser(ctx, s.Store.Pool, u.ID); err == nil {
		uu := u
		fillFromPanel(sub, &uu)
		if u.HwidDeviceLimit != nil && sub.ExtraDevices == 0 {
			sub.DeviceLimit = *u.HwidDeviceLimit
		}
		return "updated", store.UpdateSubscription(ctx, s.Store.Pool, sub)
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}

	var email *string
	if u.Email != nil && strings.TrimSpace(*u.Email) != "" {
		e := strings.ToLower(strings.TrimSpace(*u.Email))
		email = &e
	}
	if u.TelegramID == nil && email == nil {
		return "skipped", nil
	}
	result := "attached"
	var cust *store.Customer
	var err error
	if u.TelegramID != nil {
		cust, err = store.GetCustomerByTelegram(ctx, s.Store.Pool, *u.TelegramID)
	}
	if cust == nil && email != nil {
		cust, err = store.GetCustomerByEmail(ctx, s.Store.Pool, *email)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if cust == nil {
		nc := store.NewCustomer{TelegramID: u.TelegramID, Email: email, Language: "ru", Source: "panel"}
		if u.TelegramID != nil && s.Cfg.IsAdminTelegram(*u.TelegramID) {
			nc.IsAdmin = true
		}
		if cust, err = store.CreateCustomer(ctx, s.Store.Pool, nc); err != nil {
			return "", err
		}
		result = "created"
	}

	sub := &store.Subscription{CustomerID: cust.ID, Kind: "paid", Title: u.Username}
	if u.Tag != nil {
		if p := byTag[strings.ToUpper(strings.TrimSpace(*u.Tag))]; p != nil {
			sub.PlanID, sub.Title = &p.ID, p.Name
			if p.IsTrial {
				sub.Kind = "trial"
			}
		}
	}
	if u.HwidDeviceLimit != nil {
		sub.DeviceLimit = *u.HwidDeviceLimit
	}
	uu := u
	fillFromPanel(sub, &uu)
	if err := store.InsertSubscription(ctx, s.Store.Pool, sub); err != nil {
		return "", err
	}
	_ = store.AddEvent(ctx, s.Store.Pool, cust.ID, "panel_sync", map[string]any{"subscription": sub.ID, "rw_username": u.Username}, nil)
	return result, nil
}
