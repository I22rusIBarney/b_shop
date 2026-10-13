package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

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

// Guide returns installation instructions, from the Remnawave panel when configured (cached for 5 minutes).
func (s *Service) Guide(ctx context.Context) InstallGuide {
	in := s.set().Install
	if in.Source == "manual" {
		return InstallGuide{Source: "manual"}
	}
	s.guideMu.Lock()
	defer s.guideMu.Unlock()
	if s.guideCache != nil && s.guideKey == in.SubpageConfigUUID && time.Since(s.guideAt) < guideTTL {
		return InstallGuide{Source: "remnawave", Config: s.guideCache}
	}
	cfg, err := s.fetchGuide(ctx, in.SubpageConfigUUID)
	if err != nil {
		s.Log.Warn("install guide", "err", err)
		if s.guideCache != nil && s.guideKey == in.SubpageConfigUUID {
			return InstallGuide{Source: "remnawave", Config: s.guideCache} // stale is better than nothing
		}
		return InstallGuide{Source: "manual", Error: "инструкции из панели недоступны"}
	}
	s.guideCache, s.guideKey, s.guideAt = cfg, in.SubpageConfigUUID, time.Now()
	return InstallGuide{Source: "remnawave", Config: cfg}
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

// ResetGuideCache drops the cached panel guide (after settings change).
func (s *Service) ResetGuideCache() {
	s.guideMu.Lock()
	s.guideCache = nil
	s.guideMu.Unlock()
}
