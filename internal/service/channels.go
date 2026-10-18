package service

import (
	"context"
	"strconv"
	"sync"
	"time"

	"bshop/internal/settings"
	"bshop/internal/store"
)

// ChannelChecker asks Telegram whether a user is a member of a channel (implemented by the bot).
type ChannelChecker interface {
	IsChannelMember(ctx context.Context, chatID string, userID int64) (bool, error)
}

// A confirmed subscription is trusted for a while so pages do not call Telegram on every open.
const channelOKTTL = 10 * time.Minute

type channelCache struct {
	mu sync.Mutex
	ok map[string]time.Time // "<chat>|<user>" → checked at
}

var chanCache = channelCache{ok: map[string]time.Time{}}

// MissingChannels lists the required channels the customer has not joined. Admins, customers without Telegram
// and the case of Telegram being unreachable are never blocked.
func (s *Service) MissingChannels(ctx context.Context, c *store.Customer) []settings.Channel {
	cfg := s.set().Channels
	out := []settings.Channel{}
	if !cfg.Enabled || s.Channels == nil || c.TelegramID == nil || c.IsAdmin {
		return out
	}
	uid := *c.TelegramID
	for _, ch := range cfg.Items {
		key := ch.ChatID + "|" + strconv.FormatInt(uid, 10)
		chanCache.mu.Lock()
		at, ok := chanCache.ok[key]
		chanCache.mu.Unlock()
		if ok && time.Since(at) < channelOKTTL {
			continue
		}
		member, err := s.Channels.IsChannelMember(ctx, ch.ChatID, uid)
		if err != nil {
			s.Log.Warn("channel check", "channel", ch.ChatID, "err", err)
			continue // fail open: a misconfigured channel must not lock everyone out
		}
		if !member {
			out = append(out, ch)
			continue
		}
		chanCache.mu.Lock()
		chanCache.ok[key] = time.Now()
		chanCache.mu.Unlock()
	}
	return out
}
