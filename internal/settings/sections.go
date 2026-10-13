package settings

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// All is the full settings snapshot. The json name of each field is its section key.
type All struct {
	General  General  `json:"general"`
	Auth     Auth     `json:"auth"`
	Trial    Trial    `json:"trial"`
	Payments Payments `json:"payments"`
	YooKassa YooKassa `json:"yookassa"`
	MoyNalog MoyNalog `json:"moynalog"`
	SMTP     SMTP     `json:"smtp"`
	Referral Referral `json:"referral"`
}

func Defaults() All {
	return All{
		General: General{
			ShopName:              "BobrGO",
			Timezone:              "Asia/Barnaul",
			DaysInMonth:           31,
			Language:              "ru",
			MaxSubsPerCustomer:    0,
			TrafficResetThreshold: 80,
		},
		Auth: Auth{
			TelegramEnabled: true,
			EmailEnabled:    true,
			SessionDays:     60,
			OTPTTLSeconds:   300,
			OTPMaxAttempts:  5,
		},
		Trial: Trial{Enabled: false, Days: 3},
		Payments: Payments{
			AutopayEnabled:    true,
			AutopayDaysBefore: 1,
			AutopayNotifyDays: 3,
			ShowSaveCard:      true,
			PendingMinutes:    60,
		},
		YooKassa: YooKassa{
			APIURL:              "https://api.yookassa.ru/v3",
			Methods:             []string{"sbp"},
			SaveCard:            true,
			DescriptionTemplate: "Подписка {plan} на {period}",
		},
		MoyNalog: MoyNalog{
			APIURL:   "https://lknpd.nalog.ru/api/v1",
			Template: "VPN {plan} на {months} мес.",
		},
		SMTP: SMTP{Port: 587, RatePerSecond: 5},
		Referral: Referral{
			Enabled:          false,
			InviterDays:      3,
			InviteeDays:      3,
			RecurringPercent: 10,
		},
	}
}

type General struct {
	ShopName string `json:"shop_name"`
	// Timezone used for quiet hours, reports and "today" in statistics.
	Timezone    string `json:"timezone"`
	Language    string `json:"language"`
	DaysInMonth int    `json:"days_in_month"`
	// MaxSubsPerCustomer limits subscriptions per customer across all plans (0 = unlimited).
	MaxSubsPerCustomer int `json:"max_subs_per_customer"`
	// TrafficResetThreshold is the used-traffic percent after which the "reset traffic" button shows.
	TrafficResetThreshold int     `json:"traffic_reset_threshold"`
	BlockedTelegramIDs    []int64 `json:"blocked_telegram_ids"`
	MaintenanceMode       bool    `json:"maintenance_mode"`
}

func (g *General) Validate() error {
	if _, err := time.LoadLocation(g.Timezone); err != nil {
		return fmt.Errorf("неизвестный часовой пояс %q", g.Timezone)
	}
	if g.DaysInMonth < 28 || g.DaysInMonth > 31 {
		return errors.New("дней в месяце: от 28 до 31")
	}
	if g.TrafficResetThreshold < 0 || g.TrafficResetThreshold > 100 {
		return errors.New("порог сброса трафика: от 0 до 100")
	}
	return nil
}

func (g General) Location() *time.Location {
	if loc, err := time.LoadLocation(g.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

type Auth struct {
	TelegramEnabled bool   `json:"telegram_enabled"`
	EmailEnabled    bool   `json:"email_enabled"`
	YandexEnabled   bool   `json:"yandex_enabled"`
	YandexClientID  string `json:"yandex_client_id"`
	YandexSecret    string `json:"yandex_secret" secret:"true"`
	VKEnabled       bool   `json:"vk_enabled"`
	VKClientID      string `json:"vk_client_id"`
	VKSecret        string `json:"vk_secret" secret:"true"`
	SessionDays     int    `json:"session_days"`
	OTPTTLSeconds   int    `json:"otp_ttl_seconds"`
	OTPMaxAttempts  int    `json:"otp_max_attempts"`
}

func (a *Auth) Validate() error {
	if !a.TelegramEnabled && !a.EmailEnabled && !a.YandexEnabled && !a.VKEnabled {
		return errors.New("нельзя отключить все способы входа")
	}
	if a.YandexEnabled && a.YandexClientID == "" {
		return errors.New("для входа через Яндекс нужен Client ID")
	}
	if a.VKEnabled && a.VKClientID == "" {
		return errors.New("для входа через VK нужен Client ID")
	}
	if a.SessionDays < 1 {
		a.SessionDays = 60
	}
	if a.OTPTTLSeconds < 60 {
		a.OTPTTLSeconds = 300
	}
	if a.OTPMaxAttempts < 1 {
		a.OTPMaxAttempts = 5
	}
	return nil
}

type Trial struct {
	Enabled bool `json:"enabled"`
	// PlanID is the plan whose limits/squads the trial copies.
	PlanID int64 `json:"plan_id"`
	Days   int   `json:"days"`
	// RequireChannel makes trial activation wait for the mandatory channel subscription.
	AllowExtraDevices bool `json:"allow_extra_devices"`
	HidePlanChange    bool `json:"hide_plan_change"`
}

func (t *Trial) Validate() error {
	if t.Enabled && (t.PlanID == 0 || t.Days < 1) {
		return errors.New("для пробного периода выберите тариф и число дней")
	}
	return nil
}

type Payments struct {
	SuccessURL        string `json:"success_url"`
	FailURL           string `json:"fail_url"`
	AutopayEnabled    bool   `json:"autopay_enabled"`
	AutopayDaysBefore int    `json:"autopay_days_before"`
	AutopayNotifyDays int    `json:"autopay_notify_days"`
	ShowSaveCard      bool   `json:"show_save_card"`
	PendingMinutes    int    `json:"pending_minutes"`
}

func (p *Payments) Validate() error {
	if p.AutopayDaysBefore < 0 || p.AutopayNotifyDays < 0 {
		return errors.New("дни автоплатежа не могут быть отрицательными")
	}
	if p.PendingMinutes < 10 {
		p.PendingMinutes = 60
	}
	return nil
}

// YooKassa method codes: sbp, bank_card, sberbank, tinkoff_bank, yoo_money.
type YooKassa struct {
	Enabled             bool              `json:"enabled"`
	ShopID              string            `json:"shop_id"`
	SecretKey           string            `json:"secret_key" secret:"true"`
	APIURL              string            `json:"api_url"`
	Methods             []string          `json:"methods"`
	SaveCard            bool              `json:"save_card"`
	CommissionPercent   float64           `json:"commission_percent"`
	DescriptionTemplate string            `json:"description_template"`
	MethodTitles        map[string]string `json:"method_titles"`
}

var yookassaMethods = map[string]bool{"sbp": true, "bank_card": true, "sberbank": true, "tinkoff_bank": true, "yoo_money": true}

func YooKassaMethodCodes() []string {
	out := make([]string, 0, len(yookassaMethods))
	for k := range yookassaMethods {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (y *YooKassa) Validate() error {
	for _, m := range y.Methods {
		if !yookassaMethods[m] {
			return fmt.Errorf("неизвестный способ оплаты ЮKassa %q", m)
		}
	}
	if y.Enabled && (y.ShopID == "" || y.SecretKey == "") {
		return errors.New("для ЮKassa нужны Shop ID и секретный ключ")
	}
	if y.Enabled && len(y.Methods) == 0 {
		return errors.New("выберите хотя бы один способ оплаты")
	}
	if y.CommissionPercent < 0 || y.CommissionPercent > 50 {
		return errors.New("комиссия должна быть от 0 до 50%")
	}
	y.APIURL = strings.TrimRight(y.APIURL, "/")
	if y.APIURL == "" {
		y.APIURL = "https://api.yookassa.ru/v3"
	}
	return nil
}

type MoyNalog struct {
	Enabled  bool   `json:"enabled"`
	Login    string `json:"login"` // ИНН или телефон
	Password string `json:"password" secret:"true"`
	APIURL   string `json:"api_url"`
	// Template variables: {plan} {months} {days} {operation}
	Template string `json:"template"`
}

func (m *MoyNalog) Validate() error {
	if m.Enabled && (m.Login == "" || m.Password == "") {
		return errors.New("для «Мой налог» нужны ИНН/логин и пароль")
	}
	m.APIURL = strings.TrimRight(m.APIURL, "/")
	if m.APIURL == "" {
		m.APIURL = "https://lknpd.nalog.ru/api/v1"
	}
	if strings.TrimSpace(m.Template) == "" {
		m.Template = "VPN {plan} на {months} мес."
	}
	return nil
}

type SMTP struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password" secret:"true"`
	From          string `json:"from"`
	FromName      string `json:"from_name"`
	HELO          string `json:"helo"`
	RatePerSecond int    `json:"rate_per_second"`
	// ProxyURL is an optional socks5:// proxy for hosts that block SMTP ports.
	ProxyURL string `json:"proxy_url"`
}

func (s SMTP) Configured() bool { return s.Host != "" && s.From != "" }

// ReferralTier replaces the base inviter bonus once the inviter has at least MinInvited paying referrals.
type ReferralTier struct {
	MinInvited int `json:"min_invited"`
	Days       int `json:"days"`
}

type Referral struct {
	Enabled     bool `json:"enabled"`
	BaseEnabled bool `json:"base_enabled"`
	// InviterDays is credited to the inviter on the referral's first purchase.
	InviterDays int `json:"inviter_days"`
	// InviteeDays is credited to the invited customer on their first purchase (0 = none).
	InviteeDays      int            `json:"invitee_days"`
	TiersEnabled     bool           `json:"tiers_enabled"`
	Tiers            []ReferralTier `json:"tiers"`
	RecurringEnabled bool           `json:"recurring_enabled"`
	// RecurringPercent of each later purchase's days goes to the inviter (minimum 1 day).
	RecurringPercent int    `json:"recurring_percent"`
	Description      string `json:"description"`
}

func (r *Referral) Validate() error {
	sort.Slice(r.Tiers, func(i, j int) bool { return r.Tiers[i].MinInvited < r.Tiers[j].MinInvited })
	for i, t := range r.Tiers {
		if t.MinInvited < 1 || t.Days < 0 {
			return fmt.Errorf("уровень %d: число приглашённых от 1, дни не отрицательные", i+1)
		}
		if i > 0 && r.Tiers[i-1].MinInvited == t.MinInvited {
			return fmt.Errorf("два уровня с одинаковым порогом %d", t.MinInvited)
		}
	}
	if r.RecurringPercent < 0 || r.RecurringPercent > 100 {
		return errors.New("процент повторного бонуса: от 0 до 100")
	}
	return nil
}

// InviterBonus returns the days the inviter gets for a first purchase given how many paying referrals they have.
func (r Referral) InviterBonus(paidReferrals int) int {
	days := 0
	if r.BaseEnabled {
		days = r.InviterDays
	}
	if r.TiersEnabled {
		for _, t := range r.Tiers {
			if paidReferrals >= t.MinInvited {
				days = t.Days
			}
		}
	}
	return days
}
