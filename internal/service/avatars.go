package service

import (
	"context"
	"time"

	"bshop/internal/store"
)

// PhotoFetcher downloads a Telegram user's current profile photo (nil, nil = no photo or hidden by privacy settings).
type PhotoFetcher interface {
	UserPhoto(ctx context.Context, telegramID int64) ([]byte, error)
}

// Photos are refreshed after a few days; "no photo" is re-checked weekly.
const (
	avatarTTL     = 3 * 24 * time.Hour
	avatarMissTTL = 7 * 24 * time.Hour
)

// Avatar returns a customer's Telegram profile photo (JPEG) for the admin panel, or nil when there is none.
func (s *Service) Avatar(ctx context.Context, customerID int64) ([]byte, error) {
	data, fetched, ok, err := store.CachedAvatar(ctx, s.Store.Pool, customerID)
	if err != nil {
		return nil, err
	}
	ttl := avatarTTL
	if len(data) == 0 {
		ttl = avatarMissTTL
	}
	if ok && time.Since(fetched) < ttl {
		return data, nil
	}
	if s.Photos == nil {
		return data, nil
	}
	c, err := store.GetCustomer(ctx, s.Store.Pool, customerID)
	if err != nil || c.TelegramID == nil {
		return data, err
	}
	defer s.lock("avatar")() // one download at a time keeps a long customer list within Telegram's limits
	fresh, err := s.Photos.UserPhoto(ctx, *c.TelegramID)
	if err != nil {
		s.Log.Debug("avatar", "customer", customerID, "err", err)
		return data, nil // keep the old photo (or initials) when Telegram is unreachable
	}
	if err := store.SaveAvatar(ctx, s.Store.Pool, customerID, fresh); err != nil {
		return fresh, err
	}
	return fresh, nil
}
