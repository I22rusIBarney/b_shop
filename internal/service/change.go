package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bshop/internal/remnawave"
	"bshop/internal/store"

	"github.com/jackc/pgx/v5"
)

// minCharge is the smallest amount YooKassa accepts (1 ₽).
const minCharge = 100

// daysLeft rounds the time until expiry to whole days (0 if expired).
func daysLeft(expires time.Time) int {
	h := time.Until(expires).Hours()
	if h <= 0 {
		return 0
	}
	d := int((h + 12) / 24)
	if d < 1 {
		d = 1
	}
	return d
}

// ChangeQuote is one way to switch to a plan.
type ChangeQuote struct {
	Mode       string `json:"mode"` // upgrade (pay the difference) | convert | downgrade (days recalculated, free)
	DaysBefore int    `json:"days_before"`
	DaysAfter  int    `json:"days_after"`
	Amount     int64  `json:"amount"`      // to pay (0 for convert/downgrade)
	BaseAmount int64  `json:"base_amount"` // before discount
}

type ChangeTarget struct {
	PlanID         int64         `json:"plan_id"`
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	Features       []string      `json:"features"`
	TrafficLimitGB int           `json:"traffic_limit_gb"`
	DeviceLimit    int           `json:"device_limit"`
	PricePerMonth  int64         `json:"price_per_month"`
	Quotes         []ChangeQuote `json:"quotes"`
	Unavailable    string        `json:"unavailable,omitempty"`
}

type ChangeInfo struct {
	SubscriptionID  int64          `json:"subscription_id"`
	PlanID          int64          `json:"plan_id"`
	PlanName        string         `json:"plan_name"`
	TrafficLimitGB  int            `json:"traffic_limit_gb"`
	DeviceLimit     int            `json:"device_limit"`
	PricePerMonth   int64          `json:"price_per_month"`
	DaysLeft        int            `json:"days_left"`
	DiscountPercent int            `json:"discount_percent"`
	Targets         []ChangeTarget `json:"targets"`
}

// changeQuotes computes how a subscription with daysLeft on oldPlan can move to newPlan.
func changeQuotes(oldPlan, newPlan *store.Plan, days, dim, discount int) []ChangeQuote {
	oldM, newM := oldPlan.MonthlyPrice(dim), newPlan.MonthlyPrice(dim)
	if newM <= 0 || days <= 0 {
		return nil
	}
	noConvert := oldPlan.ConversionDisabled || newPlan.ConversionDisabled
	converted := days
	if oldM > 0 {
		converted = int(int64(days) * oldM / newM)
		if converted < 1 {
			converted = 1
		}
	}
	var out []ChangeQuote
	if newM > oldM {
		base := (newM - oldM) * int64(days) / int64(dim)
		amount := applyDiscount(base, discount)
		if amount < minCharge {
			amount = minCharge
		}
		out = append(out, ChangeQuote{Mode: "upgrade", DaysBefore: days, DaysAfter: days, Amount: amount, BaseAmount: base})
		if !noConvert && oldM > 0 {
			out = append(out, ChangeQuote{Mode: "convert", DaysBefore: days, DaysAfter: converted})
		}
		return out
	}
	if noConvert {
		converted = days
	}
	return append(out, ChangeQuote{Mode: "downgrade", DaysBefore: days, DaysAfter: converted})
}

// changeable loads a paid, active subscription whose plan can be changed.
func (s *Service) changeable(ctx context.Context, db store.DB, c *store.Customer, subID int64) (*store.Subscription, *store.Plan, error) {
	sub, err := store.GetSubscription(ctx, db, subID)
	if err != nil || sub.CustomerID != c.ID {
		return nil, nil, userErr("подписка не найдена")
	}
	if sub.Kind != "paid" {
		return nil, nil, userErr("на пробной подписке выберите тариф и оплатите его")
	}
	if !sub.ExpiresAt.After(time.Now()) {
		return nil, nil, userErr("подписка истекла — продлите её или оформите новую")
	}
	if sub.PlanID == nil {
		return nil, nil, userErr("у подписки нет тарифа, напишите в поддержку")
	}
	plan, err := store.GetPlan(ctx, db, *sub.PlanID)
	if err != nil {
		return nil, nil, userErr("тариф подписки не найден")
	}
	if plan.ChangeFromDisabled {
		return nil, nil, userErr("с этого тарифа перейти нельзя")
	}
	return sub, plan, nil
}

func (s *Service) targetProblem(ctx context.Context, c *store.Customer, cur, p *store.Plan) string {
	if p.ID == cur.ID || !p.IsActive || p.IsTrial || p.ChangeToDisabled || len(p.Periods) == 0 || p.Kind != "period" {
		return "-"
	}
	if p.IsHidden {
		return "-"
	}
	if p.MaxPerCustomer > 0 {
		if n, err := store.CountSubscriptions(ctx, s.Store.Pool, c.ID, &p.ID); err == nil && n >= p.MaxPerCustomer {
			return "достигнут лимит подписок этого тарифа"
		}
	}
	if p.RequiredPlanID != nil {
		return "требуется другой тариф"
	}
	return ""
}

// ChangeOptions lists plans the subscription can move to with prices and recalculated days.
func (s *Service) ChangeOptions(ctx context.Context, c *store.Customer, subID int64) (*ChangeInfo, error) {
	sub, cur, err := s.changeable(ctx, s.Store.Pool, c, subID)
	if err != nil {
		return nil, err
	}
	dim := s.set().General.DaysInMonth
	plans, err := store.ListPlans(ctx, s.Store.Pool)
	if err != nil {
		return nil, err
	}
	days := daysLeft(sub.ExpiresAt)
	info := &ChangeInfo{SubscriptionID: sub.ID, PlanID: cur.ID, PlanName: cur.Name, TrafficLimitGB: cur.TrafficLimitGB,
		DeviceLimit: cur.DeviceLimit, PricePerMonth: cur.MonthlyPrice(dim), DaysLeft: days,
		DiscountPercent: c.PersonalDiscount, Targets: []ChangeTarget{}}
	for i := range plans {
		p := &plans[i]
		problem := s.targetProblem(ctx, c, cur, p)
		if problem == "-" {
			continue
		}
		t := ChangeTarget{PlanID: p.ID, Name: p.Name, Description: p.Description, Features: p.Features,
			TrafficLimitGB: p.TrafficLimitGB, DeviceLimit: p.DeviceLimit,
			PricePerMonth: applyDiscount(p.MonthlyPrice(dim), c.PersonalDiscount), Unavailable: problem}
		if problem == "" {
			t.Quotes = changeQuotes(cur, p, days, dim, c.PersonalDiscount)
			if len(t.Quotes) == 0 {
				continue
			}
		}
		info.Targets = append(info.Targets, t)
	}
	return info, nil
}

type ChangeRequest struct {
	PlanID    int64  `json:"plan_id"`
	Mode      string `json:"mode"`
	Method    string `json:"method"`
	SaveCard  bool   `json:"save_card"`
	ReturnURL string `json:"return_url"`
}

// ChangePlan pays the difference (upgrade) or recalculates days right away (convert / downgrade).
func (s *Service) ChangePlan(ctx context.Context, c *store.Customer, subID int64, req ChangeRequest) (*BuyResult, error) {
	sub, cur, err := s.changeable(ctx, s.Store.Pool, c, subID)
	if err != nil {
		return nil, err
	}
	target, err := store.GetPlan(ctx, s.Store.Pool, req.PlanID)
	if err != nil {
		return nil, userErr("тариф не найден")
	}
	if p := s.targetProblem(ctx, c, cur, target); p != "" {
		if p == "-" {
			p = "на этот тариф перейти нельзя"
		}
		return nil, userErr(p)
	}
	dim := s.set().General.DaysInMonth
	var q *ChangeQuote
	for _, x := range changeQuotes(cur, target, daysLeft(sub.ExpiresAt), dim, c.PersonalDiscount) {
		if x.Mode == req.Mode {
			x := x
			q = &x
		}
	}
	if q == nil {
		return nil, userErr("этот способ смены тарифа недоступен")
	}
	if q.Mode == "upgrade" {
		p := &store.Payment{CustomerID: c.ID, SubscriptionID: &sub.ID, PlanID: &target.ID, FromPlanID: &cur.ID,
			Operation: "change", Title: target.Name, Days: q.DaysAfter, Months: (q.DaysAfter + dim/2) / dim,
			Amount: q.Amount, BaseAmount: q.BaseAmount, DiscountPercent: c.PersonalDiscount,
			Provider: "yookassa", Method: req.Method, SaveMethod: req.SaveCard && s.set().YooKassa.SaveCard}
		return s.startPayment(ctx, c, p, req.ReturnURL)
	}

	unlock := s.lock(fmt.Sprintf("sub:%d", sub.ID))
	defer unlock()
	var after *store.Subscription
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		cust, err := store.GetCustomerForUpdate(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		locked, err := store.GetSubscriptionForUpdate(ctx, tx, sub.ID)
		if err != nil {
			return err
		}
		if locked.PlanID == nil || *locked.PlanID != cur.ID {
			return userErr("тариф подписки уже изменился, обновите страницу")
		}
		// Recalculate under the lock with fresh data.
		days := daysLeft(locked.ExpiresAt)
		var fresh *ChangeQuote
		for _, x := range changeQuotes(cur, target, days, dim, cust.PersonalDiscount) {
			if x.Mode == req.Mode {
				x := x
				fresh = &x
			}
		}
		if fresh == nil {
			return userErr("этот способ смены тарифа недоступен")
		}
		expire := time.Now().UTC().Add(time.Duration(fresh.DaysAfter) * 24 * time.Hour)
		if err := s.switchPlan(ctx, tx, cust, locked, target, expire, fmt.Sprintf("u%d_c%d", cust.ID, time.Now().Unix())); err != nil {
			return err
		}
		after = locked
		q = fresh
		if err := store.InsertPlanChange(ctx, tx, &store.PlanChange{CustomerID: cust.ID, SubscriptionID: &locked.ID,
			FromPlanID: &cur.ID, ToPlanID: &target.ID, Mode: fresh.Mode, DaysBefore: fresh.DaysBefore, DaysAfter: fresh.DaysAfter}); err != nil {
			return err
		}
		return store.AddEvent(ctx, tx, cust.ID, "plan_change", map[string]any{"subscription": locked.ID, "from": cur.Name,
			"to": target.Name, "mode": fresh.Mode, "days_before": fresh.DaysBefore, "days_after": fresh.DaysAfter}, nil)
	})
	if err != nil {
		var ue *UserError
		if !errors.As(err, &ue) {
			s.Log.Error("change plan", "sub", sub.ID, "err", err)
			return nil, userErr("не удалось сменить тариф, попробуйте позже")
		}
		return nil, err
	}
	s.Notify.Admin(ctx, "plan_change", fmt.Sprintf("🔄 Смена тарифа (%s)\n👤 %s\n📦 %s → %s\n📅 %d → %d дн.", modeName(q.Mode),
		escape(whoLine(c)), escape(cur.Name), escape(target.Name), q.DaysBefore, q.DaysAfter), c.ID)
	return &BuyResult{Applied: true, SubscriptionID: after.ID}, nil
}

func modeName(m string) string {
	return map[string]string{"upgrade": "доплата", "convert": "конвертация", "downgrade": "понижение", "admin": "админ"}[m]
}

// switchPlan moves a subscription to another plan with the given expiry and re-applies plan limits in the panel.
func (s *Service) switchPlan(ctx context.Context, db store.DB, c *store.Customer, sub *store.Subscription, plan *store.Plan, expire time.Time, username string) error {
	if !plan.ExtraDevicesAllowed {
		sub.ExtraDevices = 0
	} else if plan.MaxExtraDevices > 0 && sub.ExtraDevices > plan.MaxExtraDevices {
		sub.ExtraDevices = plan.MaxExtraDevices
	}
	return s.provisionExtendTo(ctx, db, c, sub, Grant{Plan: plan, Kind: sub.Kind, Username: username}, expire)
}

// addExtraDevices raises the device limit of a subscription by n bought slots.
func (s *Service) addExtraDevices(ctx context.Context, db store.DB, sub *store.Subscription, n int) error {
	sub.ExtraDevices += n
	if sub.RWUserID != nil {
		limit := sub.TotalDevices()
		if _, err := s.Panel.UpdateUser(ctx, remnawave.UpdateUser{ID: *sub.RWUserID, HwidDeviceLimit: &limit}); err != nil {
			return fmt.Errorf("панель: %w", err)
		}
	}
	return store.UpdateSubscription(ctx, db, sub)
}

// DevicesOffer describes buying extra device slots for the rest of the current period.
type DevicesOffer struct {
	Allowed         bool   `json:"allowed"`
	Reason          string `json:"reason,omitempty"`
	Current         int    `json:"current"`  // total limit now
	Extra           int    `json:"extra"`    // already bought slots
	MaxMore         int    `json:"max_more"` // 0 = no limit
	DaysLeft        int    `json:"days_left"`
	PricePerMonth   int64  `json:"price_per_month"`  // per device
	PricePerDevice  int64  `json:"price_per_device"` // per device for the remaining days, after discount
	DiscountPercent int    `json:"discount_percent"`
}

func (s *Service) devicesOffer(ctx context.Context, c *store.Customer, subID int64) (*DevicesOffer, *store.Subscription, *store.Plan, error) {
	sub, err := store.GetSubscription(ctx, s.Store.Pool, subID)
	if err != nil || sub.CustomerID != c.ID {
		return nil, nil, nil, userErr("подписка не найдена")
	}
	o := &DevicesOffer{Current: sub.TotalDevices(), Extra: sub.ExtraDevices, DaysLeft: daysLeft(sub.ExpiresAt),
		DiscountPercent: c.PersonalDiscount}
	if sub.PlanID == nil {
		o.Reason = "у подписки нет тарифа"
		return o, sub, nil, nil
	}
	plan, err := store.GetPlan(ctx, s.Store.Pool, *sub.PlanID)
	if err != nil {
		return nil, nil, nil, err
	}
	o.PricePerMonth = plan.ExtraDevicePrice
	switch {
	case sub.Kind != "paid":
		o.Reason = "доступно на платной подписке"
	case !plan.ExtraDevicesAllowed || plan.ExtraDevicePrice <= 0:
		o.Reason = "на этом тарифе нельзя докупить устройства"
	case plan.DeviceLimit == 0:
		o.Reason = "на тарифе нет ограничения устройств"
	case o.DaysLeft < 1:
		o.Reason = "подписка истекла"
	case plan.MaxExtraDevices > 0 && sub.ExtraDevices >= plan.MaxExtraDevices:
		o.Reason = "куплено максимальное число устройств"
	default:
		o.Allowed = true
	}
	if plan.MaxExtraDevices > 0 {
		o.MaxMore = plan.MaxExtraDevices - sub.ExtraDevices
		if o.MaxMore < 0 {
			o.MaxMore = 0
		}
	}
	dim := s.set().General.DaysInMonth
	o.PricePerDevice = applyDiscount(plan.ExtraDevicePrice*int64(o.DaysLeft)/int64(dim), c.PersonalDiscount)
	return o, sub, plan, nil
}

func (s *Service) DevicesOffer(ctx context.Context, c *store.Customer, subID int64) (*DevicesOffer, error) {
	o, _, _, err := s.devicesOffer(ctx, c, subID)
	return o, err
}

type DevicesRequest struct {
	Count     int    `json:"count"`
	Method    string `json:"method"`
	SaveCard  bool   `json:"save_card"`
	ReturnURL string `json:"return_url"`
}

// BuyDevices starts a payment for extra device slots until the subscription's current expiry.
func (s *Service) BuyDevices(ctx context.Context, c *store.Customer, subID int64, req DevicesRequest) (*BuyResult, error) {
	o, sub, plan, err := s.devicesOffer(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	if !o.Allowed {
		return nil, userErr(o.Reason)
	}
	if req.Count < 1 || req.Count > 100 || (o.MaxMore > 0 && req.Count > o.MaxMore) {
		return nil, userErr("недопустимое число устройств")
	}
	dim := s.set().General.DaysInMonth
	base := plan.ExtraDevicePrice * int64(req.Count) * int64(o.DaysLeft) / int64(dim)
	amount := applyDiscount(base, c.PersonalDiscount)
	if amount < minCharge {
		amount = minCharge
	}
	p := &store.Payment{CustomerID: c.ID, SubscriptionID: &sub.ID, PlanID: &plan.ID, Operation: "devices",
		Title: plan.Name, Days: o.DaysLeft, Months: (o.DaysLeft + dim/2) / dim, ExtraDevices: req.Count,
		Amount: amount, BaseAmount: base, DiscountPercent: c.PersonalDiscount,
		Provider: "yookassa", Method: req.Method, SaveMethod: req.SaveCard && s.set().YooKassa.SaveCard}
	return s.startPayment(ctx, c, p, req.ReturnURL)
}

// AdminChangePlan moves a subscription to another plan; days nil keeps the current expiry.
func (s *Service) AdminChangePlan(ctx context.Context, admin *store.Customer, subID, planID int64, days *int) (*store.Subscription, error) {
	var sub *store.Subscription
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		sub, err = store.GetSubscriptionForUpdate(ctx, tx, subID)
		if err != nil {
			return userErr("подписка не найдена")
		}
		plan, err := store.GetPlan(ctx, tx, planID)
		if err != nil {
			return userErr("тариф не найден")
		}
		c, err := store.GetCustomerForUpdate(ctx, tx, sub.CustomerID)
		if err != nil {
			return err
		}
		before := daysLeft(sub.ExpiresAt)
		expire := sub.ExpiresAt
		if days != nil {
			if *days < 1 || *days > 3650 {
				return userErr("срок от 1 до 3650 дней")
			}
			expire = time.Now().UTC().Add(time.Duration(*days) * 24 * time.Hour)
		}
		from := sub.PlanID
		if err := s.switchPlan(ctx, tx, c, sub, plan, expire, fmt.Sprintf("u%d_c%d", c.ID, time.Now().Unix())); err != nil {
			return err
		}
		if err := store.InsertPlanChange(ctx, tx, &store.PlanChange{CustomerID: c.ID, SubscriptionID: &sub.ID, FromPlanID: from,
			ToPlanID: &plan.ID, Mode: "admin", DaysBefore: before, DaysAfter: daysLeft(sub.ExpiresAt)}); err != nil {
			return err
		}
		return store.AddEvent(ctx, tx, c.ID, "admin_plan_change", map[string]any{"subscription": sub.ID, "to": plan.Name}, &admin.ID)
	})
	return sub, err
}
