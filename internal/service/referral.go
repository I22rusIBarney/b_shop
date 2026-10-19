package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
				paid, err := store.PaidReferralsThrough(ctx, tx, inviter.ID, pay.ID)
				if err != nil {
					return err
				}
				if err := credit(inviter, "first", inviterDays(cfg, paid), nil); err != nil {
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
	if cb.bonus.Kind == "invitee" {
		referrer := "—"
		if r := cb.recipient.ReferrerID; r != nil {
			if rc, err := store.GetCustomer(ctx, s.Store.Pool, *r); err == nil {
				referrer = whoLine(rc)
			}
		}
		s.event(ctx, "referral_invitee", cb.recipient, map[string]any{"kind": "за первую покупку", "days": d, "referrer": referrer})
		return
	}
	kind := map[string]string{"first": "первый", "recurring": "повторный"}[cb.bonus.Kind]
	s.event(ctx, "referral_bonus", cb.recipient, map[string]any{"kind": kind, "days": d, "referral": whoLine(payer)})
}

// revokeReferral takes back the inviter's bonus days for a fully refunded payment, once per bonus.
// Days come off the subscription they went to (never below now) or off the bonus balance; whatever the
// balance no longer holds (already used by a purchase) comes off the inviter's active subscription.
// The invitee's own bonus stays with the purchase it belongs to.
func (s *Service) revokeReferral(ctx context.Context, payID uuid.UUID) error {
	type taken struct {
		to   *store.Customer
		days int
	}
	var done []taken
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		list, err := store.RevokableReferralBonuses(ctx, tx, payID)
		if err != nil {
			return err
		}
		for _, b := range list {
			to, err := store.GetCustomerForUpdate(ctx, tx, b.RecipientID)
			if err != nil {
				return err
			}
			rest := b.Days
			if b.Pending {
				take := min(rest, to.BonusDays)
				if err := store.AddBonusDays(ctx, tx, to.ID, -take); err != nil {
					return err
				}
				rest -= take
			}
			if rest > 0 {
				if sub, err := s.revokeTarget(ctx, tx, to.ID, b.SubscriptionID); err != nil {
					return err
				} else if sub != nil {
					if err := s.addDays(ctx, tx, sub, -rest); err != nil {
						return err
					}
				}
			}
			if err := store.MarkReferralBonusRevoked(ctx, tx, b.ID); err != nil {
				return err
			}
			if err := store.AddEvent(ctx, tx, to.ID, "referral_revoked", map[string]any{"days": b.Days, "payment": payID}, nil); err != nil {
				return err
			}
			done = append(done, taken{to, b.Days})
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, d := range done {
		s.Notify.Customer(ctx, d.to, Message{Text: fmt.Sprintf("↩️ Друг вернул оплату, поэтому бонус за неё отменён: <b>−%s</b>.",
			plural(d.days, "день", "дня", "дней")), ButtonText: "Открыть кабинет", ButtonURL: s.Cfg.PublicURL + "/referral",
			EmailSubject: "Бонус за друга отменён"})
	}
	return nil
}

// revokeTarget is the subscription to take bonus days from: the one they were added to, else an active one.
func (s *Service) revokeTarget(ctx context.Context, tx pgx.Tx, customerID int64, subID *int64) (*store.Subscription, error) {
	if subID != nil {
		if sub, err := store.GetSubscriptionForUpdate(ctx, tx, *subID); err == nil && sub.CustomerID == customerID {
			return sub, nil
		}
	}
	subs, err := store.ListSubscriptions(ctx, tx, customerID)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if subs[i].Active() && subs[i].RWUserID != nil {
			return store.GetSubscriptionForUpdate(ctx, tx, subs[i].ID)
		}
	}
	return nil, nil
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
	Friends     []ReferralFriend        `json:"friends"`
}

// ReferralFriend is an invited customer as the inviter sees them: a short name, never contacts or ids.
type ReferralFriend struct {
	Name   string    `json:"name"`
	Since  time.Time `json:"since"`
	Status string    `json:"status"` // joined | paid | active
	Days   int       `json:"days"`   // bonus days the inviter got from this friend (not taken back)
}

// Invitation tells an invited customer, until their first paid purchase, who invited them and the days
// that purchase will add. nil when there is nothing to show.
type Invitation struct {
	From string `json:"from"`
	Days int    `json:"days"`
}

func (s *Service) Invitation(ctx context.Context, c *store.Customer) *Invitation {
	cfg := s.set().Referral
	if !cfg.Enabled || !cfg.BaseEnabled || cfg.InviteeDays <= 0 || c.ReferrerID == nil {
		return nil
	}
	if n, err := store.PaidPurchasesBefore(ctx, s.Store.Pool, c.ID, uuid.Nil); err != nil || n > 0 {
		return nil
	}
	from, err := store.GetCustomer(ctx, s.Store.Pool, *c.ReferrerID)
	if err != nil {
		return nil
	}
	return &Invitation{From: shortName(from.FirstName, from.LastName), Days: cfg.InviteeDays}
}

// shortName is "Мария К." — first name and the last name's initial; "Друг" when there is no name.
func shortName(first, last *string) string {
	name := ""
	if first != nil {
		name = strings.TrimSpace(*first)
	}
	if name == "" {
		return "Друг"
	}
	if r := []rune(name); len(r) > 24 {
		name = string(r[:24]) + "…"
	}
	if last != nil {
		if r := []rune(strings.TrimSpace(*last)); len(r) > 0 {
			name += " " + strings.ToUpper(string(r[0])) + "."
		}
	}
	return name
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
	friends, err := store.ListReferralFriends(ctx, s.Store.Pool, c.ID, 100)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	info.Friends = make([]ReferralFriend, 0, len(friends))
	for _, f := range friends {
		st := "joined"
		if f.Active {
			st = "active"
		} else if f.Paid {
			st = "paid"
		}
		names[f.ID] = shortName(f.FirstName, f.LastName)
		info.Friends = append(info.Friends, ReferralFriend{Name: names[f.ID], Since: f.CreatedAt, Status: st, Days: f.Days})
	}
	// The history shows the same short names: no usernames, no ids of other customers.
	for i := range info.Bonuses {
		b := &info.Bonuses[i]
		b.ReferralName = "Друг"
		if b.ReferralID != nil {
			if n, ok := names[*b.ReferralID]; ok {
				b.ReferralName = n
			}
			b.ReferralID = nil
		}
		b.PaymentID, b.SubscriptionID = nil, nil
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
