package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Period struct {
	ID     int64  `db:"id" json:"id"`
	PlanID int64  `db:"plan_id" json:"-"`
	Unit   string `db:"unit" json:"unit"`
	Amount int    `db:"amount" json:"amount"`
	Price  int64  `db:"price" json:"price"`
	IsBest bool   `db:"is_best" json:"is_best"`
	Sort   int    `db:"sort" json:"sort"`
}

// Days converts the period to days using the shop's month length.
func (p Period) Days(daysInMonth int) int {
	switch p.Unit {
	case "day":
		return p.Amount
	case "week":
		return p.Amount * 7
	default:
		return p.Amount * daysInMonth
	}
}

// Months is the period expressed in (possibly fractional, rounded) months for receipts and stats.
func (p Period) Months(daysInMonth int) int {
	if p.Unit == "month" {
		return p.Amount
	}
	m := (p.Days(daysInMonth) + daysInMonth/2) / daysInMonth
	if m < 1 {
		return 0
	}
	return m
}

type Plan struct {
	ID                   int64       `db:"id" json:"id"`
	Name                 string      `db:"name" json:"name"`
	Kind                 string      `db:"kind" json:"kind"`
	Description          string      `db:"description" json:"description"`
	Features             []string    `db:"features" json:"features"`
	Sort                 int         `db:"sort" json:"sort"`
	IsActive             bool        `db:"is_active" json:"is_active"`
	IsHidden             bool        `db:"is_hidden" json:"is_hidden"`
	IsDefault            bool        `db:"is_default" json:"is_default"`
	IsTrial              bool        `db:"is_trial" json:"is_trial"`
	RenewalDisabled      bool        `db:"renewal_disabled" json:"renewal_disabled"`
	ChangeFromDisabled   bool        `db:"change_from_disabled" json:"change_from_disabled"`
	ChangeToDisabled     bool        `db:"change_to_disabled" json:"change_to_disabled"`
	ConversionDisabled   bool        `db:"conversion_disabled" json:"conversion_disabled"`
	RequiredPlanID       *int64      `db:"required_plan_id" json:"required_plan_id"`
	MaxPerCustomer       int         `db:"max_per_customer" json:"max_per_customer"`
	TrafficLimitGB       int         `db:"traffic_limit_gb" json:"traffic_limit_gb"`
	DeviceLimit          int         `db:"device_limit" json:"device_limit"`
	ExtraDevicesAllowed  bool        `db:"extra_devices_allowed" json:"extra_devices_allowed"`
	ExtraDevicePrice     int64       `db:"extra_device_price" json:"extra_device_price"`
	MaxExtraDevices      int         `db:"max_extra_devices" json:"max_extra_devices"`
	RWTag                string      `db:"rw_tag" json:"rw_tag"`
	RWInternalSquads     []uuid.UUID `db:"rw_internal_squads" json:"rw_internal_squads"`
	RWExternalSquad      *uuid.UUID  `db:"rw_external_squad" json:"rw_external_squad"`
	TrafficResetStrategy string      `db:"traffic_reset_strategy" json:"traffic_reset_strategy"`
	ResetTrafficOnBuy    bool        `db:"reset_traffic_on_buy" json:"reset_traffic_on_buy"`
	TrialDays            int         `db:"trial_days" json:"trial_days"`
	// Extra traffic packages and paid reset; only for plans with a traffic limit.
	TrafficTopupEnabled bool             `db:"traffic_topup_enabled" json:"traffic_topup_enabled"`
	TrafficPackages     []TrafficPackage `db:"traffic_packages" json:"traffic_packages"`
	TrafficResetEnabled bool             `db:"traffic_reset_enabled" json:"traffic_reset_enabled"`
	TrafficResetPrice   int64            `db:"traffic_reset_price" json:"traffic_reset_price"`
	CreatedAt            time.Time   `db:"created_at" json:"created_at"`
	Periods              []Period    `db:"-" json:"periods"`
}

func (p *Plan) TrafficBytes() int64 { return int64(p.TrafficLimitGB) << 30 }

// TrafficPackage is an extra-traffic offer: GB added to the current period for Price kopecks.
type TrafficPackage struct {
	GB    int   `json:"gb"`
	Price int64 `json:"price"`
}

// PricePerMonth is the cheapest monthly price over the plan's periods, for display.
func (p *Plan) PricePerMonth(daysInMonth int) int64 {
	var best int64 = -1
	for _, per := range p.Periods {
		d := per.Days(daysInMonth)
		if d == 0 {
			continue
		}
		pm := per.Price * int64(daysInMonth) / int64(d)
		if best < 0 || pm < best {
			best = pm
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// MonthlyPrice is the price of the shortest period normalised to one month (used for plan change math).
func (p *Plan) MonthlyPrice(daysInMonth int) int64 {
	if len(p.Periods) == 0 {
		return 0
	}
	shortest := p.Periods[0]
	for _, per := range p.Periods[1:] {
		if per.Days(daysInMonth) < shortest.Days(daysInMonth) {
			shortest = per
		}
	}
	d := shortest.Days(daysInMonth)
	if d == 0 {
		return 0
	}
	return shortest.Price * int64(daysInMonth) / int64(d)
}

func (p *Plan) Period(id int64) *Period {
	for i := range p.Periods {
		if p.Periods[i].ID == id {
			return &p.Periods[i]
		}
	}
	return nil
}

func ListPlans(ctx context.Context, db DB) ([]Plan, error) {
	plans, err := many[Plan](db.Query(ctx, `SELECT * FROM plans ORDER BY sort, id`))
	if err != nil {
		return nil, err
	}
	periods, err := many[Period](db.Query(ctx, `SELECT * FROM plan_periods ORDER BY plan_id, sort, amount`))
	if err != nil {
		return nil, err
	}
	by := map[int64][]Period{}
	for _, p := range periods {
		by[p.PlanID] = append(by[p.PlanID], p)
	}
	for i := range plans {
		plans[i].Periods = by[plans[i].ID]
		if plans[i].Periods == nil {
			plans[i].Periods = []Period{}
		}
		if plans[i].Features == nil {
			plans[i].Features = []string{}
		}
	}
	return plans, nil
}

func GetPlan(ctx context.Context, db DB, id int64) (*Plan, error) {
	p, err := one[Plan](db.Query(ctx, `SELECT * FROM plans WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	p.Periods, err = many[Period](db.Query(ctx, `SELECT * FROM plan_periods WHERE plan_id=$1 ORDER BY sort, amount`, id))
	if p.Features == nil {
		p.Features = []string{}
	}
	return p, err
}

// SavePlan inserts (ID==0) or updates a plan and replaces its periods, keeping ids of periods that still exist.
func SavePlan(ctx context.Context, tx pgx.Tx, p *Plan) error {
	if p.Features == nil {
		p.Features = []string{}
	}
	if p.RWInternalSquads == nil {
		p.RWInternalSquads = []uuid.UUID{}
	}
	if p.TrafficPackages == nil {
		p.TrafficPackages = []TrafficPackage{}
	}
	args := []any{p.Name, p.Kind, p.Description, p.Features, p.Sort, p.IsActive, p.IsHidden, p.IsTrial,
		p.RenewalDisabled, p.ChangeFromDisabled, p.ChangeToDisabled, p.ConversionDisabled, p.RequiredPlanID,
		p.MaxPerCustomer, p.TrafficLimitGB, p.DeviceLimit, p.ExtraDevicesAllowed, p.ExtraDevicePrice,
		p.MaxExtraDevices, p.RWTag, p.RWInternalSquads, p.RWExternalSquad, p.TrafficResetStrategy,
		p.ResetTrafficOnBuy, p.TrialDays, p.TrafficTopupEnabled, p.TrafficPackages, p.TrafficResetEnabled, p.TrafficResetPrice}
	if p.ID == 0 {
		if err := tx.QueryRow(ctx, `INSERT INTO plans (name, kind, description, features, sort, is_active, is_hidden, is_trial,
			renewal_disabled, change_from_disabled, change_to_disabled, conversion_disabled, required_plan_id,
			max_per_customer, traffic_limit_gb, device_limit, extra_devices_allowed, extra_device_price,
			max_extra_devices, rw_tag, rw_internal_squads, rw_external_squad, traffic_reset_strategy,
			reset_traffic_on_buy, trial_days, traffic_topup_enabled, traffic_packages, traffic_reset_enabled, traffic_reset_price)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)
			RETURNING id, created_at`, args...).Scan(&p.ID, &p.CreatedAt); err != nil {
			return err
		}
	} else {
		args = append(args, p.ID)
		tag, err := tx.Exec(ctx, `UPDATE plans SET name=$1, kind=$2, description=$3, features=$4, sort=$5, is_active=$6,
			is_hidden=$7, is_trial=$8, renewal_disabled=$9, change_from_disabled=$10, change_to_disabled=$11,
			conversion_disabled=$12, required_plan_id=$13, max_per_customer=$14, traffic_limit_gb=$15, device_limit=$16,
			extra_devices_allowed=$17, extra_device_price=$18, max_extra_devices=$19, rw_tag=$20, rw_internal_squads=$21,
			rw_external_squad=$22, traffic_reset_strategy=$23, reset_traffic_on_buy=$24, trial_days=$25,
			traffic_topup_enabled=$26, traffic_packages=$27, traffic_reset_enabled=$28, traffic_reset_price=$29 WHERE id=$30`, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	keep := []int64{}
	for i := range p.Periods {
		per := &p.Periods[i]
		per.PlanID = p.ID
		if per.Unit == "" {
			per.Unit = "month"
		}
		if per.ID != 0 {
			tag, err := tx.Exec(ctx, `UPDATE plan_periods SET unit=$1, amount=$2, price=$3, is_best=$4, sort=$5
				WHERE id=$6 AND plan_id=$7`, per.Unit, per.Amount, per.Price, per.IsBest, i, per.ID, p.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				keep = append(keep, per.ID)
				continue
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO plan_periods (plan_id, unit, amount, price, is_best, sort)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, p.ID, per.Unit, per.Amount, per.Price, per.IsBest, i).Scan(&per.ID); err != nil {
			return err
		}
		keep = append(keep, per.ID)
	}
	_, err := tx.Exec(ctx, `DELETE FROM plan_periods WHERE plan_id=$1 AND NOT (id = ANY($2))`, p.ID, keep)
	return err
}

func SetDefaultPlan(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE plans SET is_default = (id = $1)`, id)
	return err
}

func ReorderPlans(ctx context.Context, db DB, ids []int64) error {
	_, err := db.Exec(ctx, `UPDATE plans p SET sort = o.n FROM unnest($1::bigint[]) WITH ORDINALITY AS o(id, n) WHERE p.id = o.id`, ids)
	return err
}

// DeletePlan archives a plan that is referenced by subscriptions/payments, otherwise removes it.
func DeletePlan(ctx context.Context, db DB, id int64) (archived bool, err error) {
	var used bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE plan_id=$1)
		OR EXISTS(SELECT 1 FROM payments WHERE plan_id=$1)`, id).Scan(&used); err != nil {
		return false, err
	}
	if used {
		_, err = db.Exec(ctx, `UPDATE plans SET is_active=false, is_hidden=true, is_default=false WHERE id=$1`, id)
		return true, err
	}
	_, err = db.Exec(ctx, `DELETE FROM plans WHERE id=$1`, id)
	return false, err
}
