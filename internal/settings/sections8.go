package settings

import "errors"

// ---- health check ----

// Health: periodic checks of the database, Telegram, Remnawave and the enabled integrations (ЮKassa,
// «Мой налог», SMTP) plus the background jobs. A report goes to the "healthcheck" admin event route
// (Уведомления) only when a component changes state, not on every failed request.
type Health struct {
	Enabled bool `json:"enabled"`
	// IntervalSec between rounds; TimeoutSec per attempt (a failed attempt is retried once within the round).
	IntervalSec int `json:"interval_sec"`
	TimeoutSec  int `json:"timeout_sec"`
	// FailThreshold: failed rounds in a row before a component is reported down.
	FailThreshold int `json:"fail_threshold"`
	// RemindMinutes: repeat the report while a component stays down (0 = never).
	RemindMinutes int `json:"remind_minutes"`
	// NotifyRecovery sends a report when a component is back.
	NotifyRecovery bool `json:"notify_recovery"`
}

func defaultHealth() Health {
	return Health{Enabled: true, IntervalSec: 60, TimeoutSec: 10, FailThreshold: 2, RemindMinutes: 60, NotifyRecovery: true}
}

func (h *Health) Validate() error {
	switch {
	case h.IntervalSec < 30 || h.IntervalSec > 3600:
		return errors.New("период проверки: от 30 до 3600 секунд")
	case h.TimeoutSec < 2 || h.TimeoutSec > 60:
		return errors.New("тайм-аут проверки: от 2 до 60 секунд")
	case h.TimeoutSec*2+3 > h.IntervalSec:
		return errors.New("тайм-аут слишком большой для такого периода: проверка с повтором должна укладываться в период")
	case h.FailThreshold < 1 || h.FailThreshold > 10:
		return errors.New("порог ошибок: от 1 до 10 проверок подряд")
	case h.RemindMinutes < 0 || h.RemindMinutes > 1440:
		return errors.New("напоминание: от 0 (не напоминать) до 1440 минут")
	}
	return nil
}
