package service

import (
	"context"
	"errors"
	"fmt"

	"bshop/internal/settings"
	"bshop/internal/store"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type creditedBonus struct {
	recipient *store.Customer
	bonus     store.ReferralBonus
	subTitle  string
}

// inviterDays is the bonus for a referral's first purchase: the highest reached tier replaces the base bonus.
func inviterDays(r settings.Referral, paidReferrals int) int {
	days := r.InviterDays
	if r.TiersEnabled {
		for _, t := range r.Tiers { // sorted by MinInvited
			if paidReferrals >= t.MinInvited {
				days = t.Days
			}
		}
	}
	return days
}

// recurringDays is the inviter's share of a later purchase (at least one day).
func recurringDays(r settings.Referral, purchaseDays int) int {
	d := purchaseDays * r.RecurringPercent / 100
	if d < 1 {
		d = 1
	}
	return d
}

// ProcessReferral credits referral bonuses for an applied payment exactly once.
func (s *Service) ProcessReferral(ctx context.Context, id uuid.UUID) error {
	unlock := s.lock("ref:" + id.String())
	defer unlock()
	var credited []creditedBonus
	var payer *store.Customer
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		pay, err := store.GetPaymentForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if pay.ReferralDone || pay.AppliedAt == nil {
			return nil
		}
		cfg := s.set().Referral
		eligible := pay.Status == "succeeded" && pay.Provider != "free" &&
			(pay.Operation == "buy" || pay.Operation == "renew" || pay.Operation == "autopay")
		if !eligible || !cfg.Enabled {
			return store.MarkReferralDone(ctx, tx, pay.ID)
		}
		payer, err = store.GetCustomer(ctx, tx, pay.CustomerID)
		if err != nil {
			return err
		}
		if payer.ReferrerID == nil || *payer.ReferrerID == payer.ID {
			return store.MarkReferralDone(ctx, tx, pay.ID)
		}
		inviter, err := store.GetCustomerForUpdate(ctx, tx, *payer.ReferrerID)
		if errors.Is(err, store.ErrNotFound) {
			return store.MarkReferralDone(ctx, tx, pay.ID)
		} else if err != nil {
			return err
		}
		before, err := store.PaidPurchasesBefore(ctx, tx, payer.ID, pay.ID)
		if err != nil {
			return err
		}
		credit := func(to *store.Customer, kind string, days int, preferSub *int64) error {
			if days <= 0 {
				return nil
			}
			b := store.ReferralBonus{RecipientID: to.ID, ReferralID: &payer.ID, PaymentID: &pay.ID, Kind: kind, Days: days}
			title, err := s.creditDays(ctx, tx, to, &b, preferSub)
			if err != nil || title == "" && !b.Pending {
				return err
			}
			credited = append(credited, creditedBonus{recipient: to, bonus: b, subTitle: title})
			return nil
		}
		if before == 0 {
			if cfg.BaseEnabled {
				stats, err := store.GetReferralStats(ctx, tx, inviter.ID)
				if err != nil {
					return err
				}
				if err := credit(inviter, "first", inviterDays(cfg, stats.Paid), nil); err != nil {
					return err
				}
				if err := credit(payer, "invitee", cfg.InviteeDays, pay.SubscriptionID); err != nil {
					return err
				}
			}
		} else if cfg.RecurringEnabled && cfg.RecurringPercent > 0 {
			if err := credit(inviter, "recurring", recurringDays(cfg, pay.Days), nil); err != nil {
				return err
			}
		}
		return store.MarkReferralDone(ctx, tx, pay.ID)
	})
	if err != nil {
		return err
	}
	for _, cb := range credited {
		s.notifyBonus(ctx, payer, cb)
	}
	return nil
}

// creditDays adds bonus days to the recipient's subscription (preferred, else primary, else any provisioned one)
// or to the bonus balance if they have none. Returns the subscription title ("" with b.Pending for the balance).
// A duplicate bonus (already credited for this payment) returns "" and no error.
func (s *Service) creditDays(ctx context.Context, tx pgx.Tx, to *store.Customer, b *store.ReferralBonus, preferSub *int64) (string, error) {
	subs, err := store.ListSubscriptions(ctx, tx, to.ID)
	if err != nil {
		return "", err
	}
	var target *store.Subscription
	for i := range subs {
		sb := &subs[i]
		if sb.RWUserID == nil {
			continue
		}
		if preferSub != nil && sb.ID == *preferSub {
			target = sb
			break
		}
		if target == nil || (!target.Active() && sb.Active()) {
			target = sb
		}
	}
	if target == nil {
		b.Pending = true
	} else {
		b.SubscriptionID = &target.ID
	}
	inserted, err := store.InsertReferralBonus(ctx, tx, b)
	if err != nil || !inserted {
		b.Pending = false
		return "", err
	}
	if target == nil {
		return "", store.AddBonusDays(ctx, tx, to.ID, b.Days)
	}
	locked, err := store.GetSubscriptionForUpdate(ctx, tx, target.ID)
	if err != nil {
		return "", err
	}
	if err := s.addDays(ctx, tx, locked, b.Days); err != nil {
		return "", err
	}
	return locked.Title, nil
}

func (s *Service) notifyBonus(ctx context.Context, payer *store.Customer, cb creditedBonus) {
	d := plural(cb.bonus.Days, "день", "дня", "дней")
	where := "к подписке «" + escape(cb.subTitle) + "»"
	if cb.bonus.Pending {
		where = "— они добавятся к вашей первой подписке"
	}
	var text string
	switch cb.bonus.Kind {
	case "invitee":
		text = fmt.Sprintf("🎁 Бонус за регистрацию по приглашению: <b>+%s</b> %s.", d, where)
	case "first":
		text = fmt.Sprintf("🎁 Ваш друг оформил подписку! Начислено <b>+%s</b> %s.", d, where)
	default:
		text = fmt.Sprintf("🎁 Ваш друг продлил подписку. Начислено <b>+%s</b> %s.", d, where)
	}
	s.Notify.Customer(ctx, cb.recipient, Message{Text: text, ButtonText: "Открыть кабинет", ButtonURL: s.Cfg.PublicURL + "/",
		EmailSubject: "Бонусные дни"})
	kind := map[string]string{"first": "за первую оплату", "recurring": "за повторную оплату", "invitee": "приглашённому"}[cb.bonus.Kind]
	s.Notify.Admin(ctx, "referral_bonus", fmt.Sprintf("🎁 Реферальный бонус %s: +%s\n👤 Получатель: %s\n🤝 За: %s", kind, d,
		escape(whoLine(cb.recipient)), escape(whoLine(payer))), cb.recipient.ID)
}

// AdminReferralBonus credits manual bonus days to a customer.
func (s *Service) AdminReferralBonus(ctx context.Context, admin *store.Customer, customerID int64, days int) error {
	if days < 1 || days > 3650 {
		return userErr("укажите число дней")
	}
	return s.Store.Tx(ctx, func(tx pgx.Tx) error {
		to, err := store.GetCustomerForUpdate(ctx, tx, customerID)
		if err != nil {
			return err
		}
		b := store.ReferralBonus{RecipientID: to.ID, Kind: "manual", Days: days, ActorID: &admin.ID}
		if _, err := s.creditDays(ctx, tx, to, &b, nil); err != nil {
			return err
		}
		return store.AddEvent(ctx, tx, to.ID, "referral_manual", map[string]any{"days": days}, &admin.ID)
	})
}

// ReferralInfo is the cabinet's referral page.
type ReferralInfo struct {
	Enabled     bool                    `json:"enabled"`
	Link        string                  `json:"link"`
	WebLink     string                  `json:"web_link"`
	Stats       store.ReferralStats     `json:"stats"`
	Bonuses     []store.ReferralBonus   `json:"bonuses"`
	Description string                  `json:"description"`
	InviterDays int                     `json:"inviter_days"`
	InviteeDays int                     `json:"invitee_days"`
	Recurring   int                     `json:"recurring_percent"`
	Tiers       []settings.ReferralTier `json:"tiers"`
	NextTier    *settings.ReferralTier  `json:"next_tier,omitempty"`
	CurrentDays int                     `json:"current_days"` // bonus for the next referral's first purchase
	BonusDays   int                     `json:"bonus_days"`   // waiting on the balance
}

func (s *Service) Referral(ctx context.Context, c *store.Customer, tgLink string) (*ReferralInfo, error) {
	cfg := s.set().Referral
	info := &ReferralInfo{Enabled: cfg.Enabled, Link: tgLink, Description: cfg.Description, BonusDays: c.BonusDays,
		Tiers: []settings.ReferralTier{}}
	if c.RefCode != nil {
		info.WebLink = s.Cfg.PublicURL + "/?startapp=ref_" + *c.RefCode
	}
	if cfg.BaseEnabled {
		info.InviterDays, info.InviteeDays = cfg.InviterDays, cfg.InviteeDays
	}
	if cfg.RecurringEnabled {
		info.Recurring = cfg.RecurringPercent
	}
	var err error
	if info.Stats, err = store.GetReferralStats(ctx, s.Store.Pool, c.ID); err != nil {
		return nil, err
	}
	if info.Bonuses, err = store.ListReferralBonuses(ctx, s.Store.Pool, c.ID, 50); err != nil {
		return nil, err
	}
	if cfg.TiersEnabled {
		info.Tiers = cfg.Tiers
		for i := range cfg.Tiers {
			if cfg.Tiers[i].MinInvited > info.Stats.Paid {
				t := cfg.Tiers[i]
				info.NextTier = &t
				break
			}
		}
	}
	if cfg.BaseEnabled {
		info.CurrentDays = inviterDays(cfg, info.Stats.Paid+1)
	}
	return info, nil
}
