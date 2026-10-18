package service

import (
	"context"
	"strings"

	"bshop/internal/store"
)

var bannerIcons = map[string]bool{"info": true, "gift": true, "star": true, "warning": true, "mail": true, "telegram": true, "news": true}
var bannerSegments = map[string]bool{"all": true, "active": true, "trial": true, "expired": true, "no_sub": true}

// ValidateBanner normalises a banner from the admin UI.
func ValidateBanner(b *store.Banner) error {
	b.Title = strings.TrimSpace(b.Title)
	b.Subtitle = strings.TrimSpace(b.Subtitle)
	b.ButtonText = strings.TrimSpace(b.ButtonText)
	b.ButtonURL = strings.TrimSpace(b.ButtonURL)
	if b.Title == "" {
		return userErr("укажите заголовок")
	}
	if len([]rune(b.Title)) > 80 || len([]rune(b.Subtitle)) > 200 || len([]rune(b.ButtonText)) > 30 {
		return userErr("слишком длинный текст: заголовок до 80, подзаголовок до 200, кнопка до 30 символов")
	}
	if !bannerIcons[b.Icon] {
		b.Icon = "info"
	}
	if !bannerSegments[b.Segment] {
		b.Segment = "all"
	}
	if b.ButtonURL != "" && !strings.HasPrefix(b.ButtonURL, "/") && !strings.HasPrefix(b.ButtonURL, "https://") &&
		!strings.HasPrefix(b.ButtonURL, "tg://") {
		return userErr("ссылка кнопки: /путь в кабинете, https://… или tg://…")
	}
	if b.ButtonText != "" && b.ButtonURL == "" && b.Kind == "" {
		return userErr("для кнопки нужна ссылка")
	}
	if b.StartsAt != nil && b.EndsAt != nil && !b.EndsAt.After(*b.StartsAt) {
		return userErr("«Показывать до» должно быть позже «Показывать с»")
	}
	return nil
}

// BannersFor returns the banners this customer should see now.
func (s *Service) BannersFor(ctx context.Context, c *store.Customer) ([]store.Banner, error) {
	list, err := store.ActiveBannersFor(ctx, s.Store.Pool, c.ID)
	if err != nil {
		return nil, err
	}
	seg := ""
	segment := func() string {
		if seg != "" {
			return seg
		}
		seg = "no_sub"
		subs, _ := store.ListSubscriptions(ctx, s.Store.Pool, c.ID)
		for _, sb := range subs {
			switch {
			case sb.Active() && sb.Kind != "trial":
				seg = "active"
				return seg
			case sb.Active():
				seg = "trial"
			case seg == "no_sub":
				seg = "expired"
			}
		}
		return seg
	}
	auth := s.set().Auth
	out := make([]store.Banner, 0, len(list))
	for _, b := range list {
		switch b.Kind {
		case "link_email":
			if c.Email != nil || !auth.EmailEnabled {
				continue
			}
		case "link_telegram":
			if c.TelegramID != nil || !auth.TelegramEnabled {
				continue
			}
		}
		if b.Segment != "all" && b.Segment != segment() {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *Service) DismissBanner(ctx context.Context, c *store.Customer, id int64) error {
	return store.DismissBanner(ctx, s.Store.Pool, id, c.ID)
}
