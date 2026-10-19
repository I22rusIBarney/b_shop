package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"bshop/internal/store"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var promoCodeRe = regexp.MustCompile(`^[A-Z0-9_-]{3,32}$`)

// NormalizePromoCode upper-cases and trims a code as typed by a customer.
func NormalizePromoCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// ValidatePromo checks an admin-edited promo code.
func ValidatePromo(p *store.Promo) error {
	p.Code = NormalizePromoCode(p.Code)
	p.Folder = strings.TrimSpace(p.Folder)
	if !promoCodeRe.MatchString(p.Code) {
		return userErr("код: 3–32 символа, латиница, цифры, «-» и «_»")
	}
	switch p.Kind {
	case "discount":
		if p.Value < 1 || p.Value > 100 {
			return userErr("скидка от 1 до 100%")
		}
	case "days":
		if p.Value < 1 || p.Value > 3650 {
			return userErr("бонусные дни от 1 до 3650")
		}
	case "unlock":
		if len(p.PlanIDs) == 0 {
			return userErr("выберите тарифы, которые откроет промокод")
		}
		p.Value = 1
	default:
		return userErr("неизвестный тип промокода")
	}
	if p.MaxUses < 0 || p.ReserveHours < 0 {
		return userErr("лимиты не могут быть отрицательными")
	}
	if p.ValidFrom != nil && p.ValidUntil != nil && !p.ValidUntil.After(*p.ValidFrom) {
		return userErr("дата окончания раньше даты начала")
	}
	return nil
}

// GeneratePromoCode returns a random code with an optional prefix.
func GeneratePromoCode(prefix string) string {
	prefix = NormalizePromoCode(prefix)
	return prefix + store.RandomCode(8)
}

// RedeemResult tells the cabinet what happened after entering a code.
type RedeemResult struct {
	Kind           string     `json:"kind"`
	Value          int        `json:"value"`
	Code           string     `json:"code"`
	SubscriptionID *int64     `json:"subscription_id,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Pending        bool       `json:"pending"`            // days went to the balance (no subscription yet)
	PlanIDs        []int64    `json:"plan_ids,omitempty"` // unlock: plans opened
	Message        string     `json:"message"`
}

// checkPromo validates that the customer may use the code now. Must run in a tx with the promo row locked.
func (s *Service) checkPromo(ctx context.Context, db store.DB, c *store.Customer, p *store.Promo) error {
	now := time.Now()
	switch {
	case !p.IsActive:
		return userErr("промокод не действует")
	case p.ValidFrom != nil && now.Before(*p.ValidFrom):
		return userErr("промокод ещё не начал действовать")
	case p.ValidUntil != nil && now.After(*p.ValidUntil):
		return userErr("срок действия промокода истёк")
	case p.CustomerID != nil && *p.CustomerID != c.ID:
		return userErr("промокод не найден")
	case p.MaxUses > 0 && p.Used+p.Reserved >= p.MaxUses:
		return userErr("промокод закончился")
	}
	if r, err := store.GetRedemption(ctx, db, p.ID, c.ID); err == nil {
		if r.Status == "reserved" {
			return userErr("промокод уже активирован — скидка применится при оплате")
		}
		return userErr("вы уже использовали этот промокод")
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if p.NewCustomersOnly {
		n, err := store.PaidPurchasesBefore(ctx, db, c.ID, uuid.Nil)
		if err != nil {
			return err
		}
		if n > 0 {
			return userErr("промокод только для новых клиентов")
		}
	}
	return nil
}

// RedeemPromo activates a code for the customer: bonus days are added right away,
// a discount is reserved and applied to the next suitable payment.
func (s *Service) RedeemPromo(ctx context.Context, c *store.Customer, code string, subID int64) (*RedeemResult, error) {
	code = NormalizePromoCode(code)
	if code == "" {
		return nil, userErr("введите промокод")
	}
	unlock := s.lock(fmt.Sprintf("promo:%d", c.ID))
	defer unlock()
	var res *RedeemResult
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		p, err := store.GetPromoByCode(ctx, tx, code)
		if errors.Is(err, store.ErrNotFound) {
			return userErr("промокод не найден")
		} else if err != nil {
			return err
		}
		if p, err = store.GetPromoForUpdate(ctx, tx, p.ID); err != nil {
			return err
		}
		cust, err := store.GetCustomerForUpdate(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if err := s.checkPromo(ctx, tx, cust, p); err != nil {
			return err
		}
		res = &RedeemResult{Kind: p.Kind, Value: p.Value, Code: p.Code}
		r := &store.Redemption{PromoID: p.ID, CustomerID: cust.ID}
		if p.Kind == "unlock" {
			if _, err := store.UnlockPlans(ctx, tx, cust.ID, p.PlanIDs, &p.ID); err != nil {
				return err
			}
			names := []string{}
			rows, err := tx.Query(ctx, `SELECT name FROM plans WHERE id = ANY($1) ORDER BY sort, id`, p.PlanIDs)
			if err != nil {
				return err
			}
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					rows.Close()
					return err
				}
				names = append(names, "«"+n+"»")
			}
			rows.Close()
			r.Status = "used"
			if err := store.InsertRedemption(ctx, tx, r); err != nil {
				return err
			}
			res.PlanIDs = p.PlanIDs
			if len(names) == 1 {
				res.Message = "Вам открыт тариф " + names[0] + " — он появился в списке тарифов"
			} else {
				res.Message = "Вам открыты тарифы " + strings.Join(names, ", ") + " — они появились в списке тарифов"
			}
			return store.AddEvent(ctx, tx, cust.ID, "promo", map[string]any{"code": p.Code, "kind": p.Kind, "value": p.Value, "plans": names}, nil)
		}
		if p.Kind == "discount" {
			r.Status = "reserved"
			if err := store.InsertRedemption(ctx, tx, r); err != nil {
				return err
			}
			res.Message = fmt.Sprintf("Скидка %d%% применится к следующей оплате", p.Value)
			return store.AddEvent(ctx, tx, cust.ID, "promo", map[string]any{"code": p.Code, "kind": p.Kind, "value": p.Value}, nil)
		}
		// Bonus days: to the chosen subscription, else the primary one, else the balance.
		sub, err := s.promoTarget(ctx, tx, cust, p, subID)
		if err != nil {
			return err
		}
		r.Status, r.Days = "used", p.Value
		if sub == nil {
			if err := store.AddBonusDays(ctx, tx, cust.ID, p.Value); err != nil {
				return err
			}
			res.Pending = true
			res.Message = fmt.Sprintf("+%s добавятся к первой подписке", plural(p.Value, "день", "дня", "дней"))
		} else {
			if err := s.addDays(ctx, tx, sub, p.Value); err != nil {
				return err
			}
			r.SubscriptionID = &sub.ID
			res.SubscriptionID, res.ExpiresAt = &sub.ID, &sub.ExpiresAt
			res.Message = fmt.Sprintf("+%s к подписке «%s»", plural(p.Value, "день", "дня", "дней"), sub.Title)
		}
		if err := store.InsertRedemption(ctx, tx, r); err != nil {
			return err
		}
		return store.AddEvent(ctx, tx, cust.ID, "promo", map[string]any{"code": p.Code, "kind": p.Kind, "value": p.Value, "subscription": r.SubscriptionID}, nil)
	})
	if err != nil {
		var ue *UserError
		if !errors.As(err, &ue) {
			s.Log.Error("redeem promo", "customer", c.ID, "err", err)
			return nil, userErr("не удалось применить промокод, попробуйте позже")
		}
		return nil, err
	}
	what := fmt.Sprintf("скидка %d%%", res.Value)
	switch res.Kind {
	case "days":
		what = "+" + plural(res.Value, "день", "дня", "дней")
	case "unlock":
		what = "Разблокировка тарифа"
		var names []string
		for _, id := range res.PlanIDs {
			id := id
			if n := s.planName(ctx, &id); n != "—" {
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			what += " (" + strings.Join(names, ", ") + ")"
		}
	}
	s.event(ctx, "promo", c, map[string]any{"code": res.Code, "what": what})
	return res, nil
}

func (s *Service) promoTarget(ctx context.Context, tx pgx.Tx, c *store.Customer, p *store.Promo, subID int64) (*store.Subscription, error) {
	subs, err := store.ListSubscriptions(ctx, tx, c.ID)
	if err != nil {
		return nil, err
	}
	var pick *store.Subscription
	for i := range subs {
		sb := &subs[i]
		if subID != 0 {
			if sb.ID == subID {
				pick = sb
			}
			continue
		}
		if pick == nil && sb.RWUserID != nil {
			pick = sb // ordered: primary first
		}
	}
	if subID != 0 && pick == nil {
		return nil, userErr("подписка не найдена")
	}
	if pick == nil {
		return nil, nil
	}
	if pick.PlanID == nil || !p.AppliesToPlan(*pick.PlanID) {
		return nil, userErr("промокод не подходит к тарифу этой подписки")
	}
	return store.GetSubscriptionForUpdate(ctx, tx, pick.ID)
}

// ActivePromos are the reserved discounts shown in the cabinet.
func (s *Service) ActivePromos(ctx context.Context, c *store.Customer) ([]store.Redemption, error) {
	return store.ActiveDiscounts(ctx, s.Store.Pool, c.ID)
}

// discountFor picks the best discount for a plan: personal or a reserved promo code (they do not add up).
func (s *Service) discountFor(ctx context.Context, db store.DB, c *store.Customer, planID int64) (percent int, code string) {
	percent = c.PersonalDiscount
	list, err := store.ActiveDiscounts(ctx, db, c.ID)
	if err != nil {
		return percent, ""
	}
	for _, r := range list {
		p := store.Promo{PlanIDs: r.PlanIDs}
		if p.AppliesToPlan(planID) && r.Value > percent {
			return r.Value, r.Code
		}
	}
	return percent, ""
}
