package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"bshop/internal/store"

	"github.com/google/uuid"
)

// InstallGuide is what the cabinet's install screen renders.
type InstallGuide struct {
	Source string `json:"source"` // remnawave | manual
	// Config is the panel's subscription page config as is (platforms → apps → blocks with buttons).
	Config json.RawMessage `json:"config,omitempty"`
	Error  string          `json:"error,omitempty"`
}

const guideTTL = 5 * time.Minute

// Guide returns installation instructions for the customer's subscription, from the Remnawave panel when
// configured (cached for 5 minutes per config). Like the panel's own subscription page, a config assigned to
// the subscription's external squad wins; then the one picked in the admin; then the panel's first config.
func (s *Service) Guide(ctx context.Context, c *store.Customer, subID int64) InstallGuide {
	in := s.set().Install
	if in.Source == "manual" {
		return InstallGuide{Source: "manual"}
	}
	want := in.SubpageConfigUUID
	if id := s.squadSubpage(ctx, c, subID); id != "" {
		want = id
	}
	s.guideMu.Lock()
	defer s.guideMu.Unlock()
	if s.guides == nil {
		s.guides = map[string]cachedGuide{}
	}
	if g, ok := s.guides[want]; ok && time.Since(g.at) < guideTTL {
		return InstallGuide{Source: "remnawave", Config: g.cfg}
	}
	cfg, err := s.fetchGuide(ctx, want)
	if err != nil {
		s.Log.Warn("install guide", "config", want, "err", err)
		if g, ok := s.guides[want]; ok {
			return InstallGuide{Source: "remnawave", Config: g.cfg} // stale is better than nothing
		}
		return InstallGuide{Source: "manual", Error: "инструкции из панели недоступны"}
	}
	s.guides[want] = cachedGuide{cfg: cfg, at: time.Now()}
	return InstallGuide{Source: "remnawave", Config: cfg}
}

type cachedGuide struct {
	cfg json.RawMessage
	at  time.Time
}

// squadSubpage returns the subscription page config of the subscription's external squad, if any.
func (s *Service) squadSubpage(ctx context.Context, c *store.Customer, subID int64) string {
	if c == nil {
		return ""
	}
	var sub *store.Subscription
	if subID > 0 {
		if x, err := store.GetSubscription(ctx, s.Store.Pool, subID); err == nil && x.CustomerID == c.ID {
			sub = x
		}
	}
	if sub == nil {
		subs, err := store.ListSubscriptions(ctx, s.Store.Pool, c.ID)
		if err != nil || len(subs) == 0 {
			return ""
		}
		sub = &subs[0]
		for i := range subs {
			if subs[i].IsPrimary {
				sub = &subs[i]
			}
		}
	}
	// The squad the user actually has in the panel; the plan's squad if the panel can't be asked.
	var squad *uuid.UUID
	if sub.RWUserID != nil {
		if u, err := s.Panel.GetUser(ctx, *sub.RWUserID); err == nil {
			squad = u.ExternalSquadUUID
		}
	}
	if squad == nil && sub.PlanID != nil {
		if p, err := store.GetPlan(ctx, s.Store.Pool, *sub.PlanID); err == nil {
			squad = p.RWExternalSquad
		}
	}
	if squad == nil {
		return ""
	}
	s.guideMu.Lock()
	fresh := s.squadsAt.After(time.Now().Add(-guideTTL))
	m := s.squadSubpages
	s.guideMu.Unlock()
	if !fresh {
		list, err := s.Panel.ExternalSquads(ctx)
		if err != nil {
			s.Log.Warn("external squads", "err", err)
		} else {
			m = map[uuid.UUID]string{}
			for _, q := range list {
				if q.SubpageConfigUUID != nil {
					m[q.UUID] = q.SubpageConfigUUID.String()
				}
			}
			s.guideMu.Lock()
			s.squadSubpages, s.squadsAt = m, time.Now()
			s.guideMu.Unlock()
		}
	}
	return m[*squad]
}

func (s *Service) fetchGuide(ctx context.Context, want string) (json.RawMessage, error) {
	id, err := uuid.Parse(want)
	if want == "" || err != nil {
		list, err := s.Panel.SubpageConfigs(ctx)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, errors.New("в панели нет конфигураций страницы подписки")
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ViewPosition < list[j].ViewPosition })
		id = list[0].UUID
	}
	cfg, err := s.Panel.SubpageConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(cfg) == 0 || string(cfg) == "null" {
		return nil, errors.New("конфигурация страницы подписки пустая")
	}
	return cfg, nil
}

// ResetGuideCache drops the cached panel guides (after settings change).
func (s *Service) ResetGuideCache() {
	s.guideMu.Lock()
	s.guides, s.squadsAt = nil, time.Time{}
	s.guideMu.Unlock()
}
