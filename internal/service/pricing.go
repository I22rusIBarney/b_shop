package service

import "bshop/internal/store"

// MaxPersonalMarkup caps the per-customer markup (%), same as the DB check.
const MaxPersonalMarkup = 300

// applyMarkup raises a price by pct percent, rounding up to the kopeck.
func applyMarkup(price int64, pct int) int64 {
	if pct <= 0 || price <= 0 {
		return price
	}
	return (price*int64(100+pct) + 99) / 100
}

// personalPlan is the plan as this customer sees it: period and extra-device prices carry the personal
// markup, so every formula built on them (purchase, renewal, autopay, plan change, devices, gifts) charges
// it exactly once. The copy is for pricing only — never save it or provision from it.
// Traffic packs and resets stay at list price, like they ignore discounts.
func personalPlan(p *store.Plan, c *store.Customer) *store.Plan {
	if p == nil || c == nil || c.PersonalMarkup <= 0 {
		return p
	}
	cp := *p
	cp.Periods = make([]store.Period, len(p.Periods))
	for i, per := range p.Periods {
		per.Price = applyMarkup(per.Price, c.PersonalMarkup)
		cp.Periods[i] = per
	}
	cp.ExtraDevicePrice = applyMarkup(p.ExtraDevicePrice, c.PersonalMarkup)
	return &cp
}
