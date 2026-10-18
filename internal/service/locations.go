package service

import (
	"context"
	"fmt"
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

// serverStatusContext tells the assistant which servers are up or down (names only, cached like Locations).
func (s *Service) serverStatusContext(ctx context.Context) string {
	s.locMu.Lock()
	stale := s.locCache == nil || time.Since(s.locAt) > locationsTTL
	s.locMu.Unlock()
	if stale {
		nodes, err := s.Panel.Nodes(ctx)
		if err != nil {
			return "Не удалось получить состояние серверов из панели."
		}
		list := make([]Location, 0, len(nodes))
		for _, n := range nodes {
			if !n.IsDisabled {
				list = append(list, Location{Name: strings.TrimSpace(n.Name), Country: strings.ToUpper(n.CountryCode), Online: n.IsConnected})
			}
		}
		s.locMu.Lock()
		s.locCache, s.locAt = list, time.Now()
		s.locMu.Unlock()
	}
	s.locMu.Lock()
	defer s.locMu.Unlock()
	var up, down []string
	for _, l := range s.locCache {
		if l.Online {
			up = append(up, l.Name)
		} else {
			down = append(down, l.Name)
		}
	}
	if len(up)+len(down) == 0 {
		return ""
	}
	out := fmt.Sprintf("Работают (%d): %s.", len(up), strings.Join(up, ", "))
	if len(down) > 0 {
		out += fmt.Sprintf("\nНЕ работают (%d): %s. Если клиент жалуется на эти локации — это известная проблема, предложите другую локацию.",
			len(down), strings.Join(down, ", "))
	}
	return out
}
