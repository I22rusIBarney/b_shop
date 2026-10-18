package service

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Location is what a customer may see about a node: never its address.
type Location struct {
	Name    string `json:"name"`
	Country string `json:"country"`
	Online  bool   `json:"online"`
}

const locationsTTL = time.Minute

// Locations lists panel nodes for the cabinet. Disabled nodes are hidden; offline ones only if enabled in settings.
func (s *Service) Locations(ctx context.Context) ([]Location, error) {
	cfg := s.set().Locations
	if !cfg.Enabled {
		return nil, userErr("раздел «Локации» отключён")
	}
	s.locMu.Lock()
	defer s.locMu.Unlock()
	if s.locCache == nil || time.Since(s.locAt) > locationsTTL {
		nodes, err := s.Panel.Nodes(ctx)
		if err != nil {
			if s.locCache != nil {
				s.Log.Warn("locations: panel unavailable, showing cached list", "err", err)
			} else {
				return nil, userErr("не удалось получить список серверов, попробуйте позже")
			}
		} else {
			list := make([]Location, 0, len(nodes))
			for _, n := range nodes {
				if n.IsDisabled {
					continue
				}
				list = append(list, Location{Name: strings.TrimSpace(n.Name), Country: strings.ToUpper(n.CountryCode), Online: n.IsConnected})
			}
			sort.SliceStable(list, func(i, j int) bool {
				if list[i].Online != list[j].Online {
					return list[i].Online
				}
				return list[i].Name < list[j].Name
			})
			s.locCache, s.locAt = list, time.Now()
		}
	}
	out := make([]Location, 0, len(s.locCache))
	for _, l := range s.locCache {
		if l.Online || cfg.ShowOffline {
			out = append(out, l)
		}
	}
	return out, nil
}
