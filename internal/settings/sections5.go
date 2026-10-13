package settings

import (
	"errors"
	"fmt"
	"regexp"
)

// ---- Remnawave webhooks ----

// CustomerNotice is a customer message for one panel event. Empty template = built-in text.
type CustomerNotice struct {
	Enabled  bool   `json:"enabled"`
	Template string `json:"template"`
}

type Webhooks struct {
	Enabled bool `json:"enabled"`
	// Secret is WEBHOOK_SECRET_HEADER from the panel's .env: payloads are signed with HMAC-SHA256 of the body.
	Secret string `json:"secret" secret:"true"`
	// Customer messages by event key (see service.CustomerWebhookEvents). Missing key = built-in default.
	Customer map[string]CustomerNotice `json:"customer"`
	// ThresholdQuietHours: don't repeat the same traffic/expiry notice to a customer within this many hours.
	RepeatQuietHours int `json:"repeat_quiet_hours"`
}

func defaultWebhooks() Webhooks {
	return Webhooks{Customer: map[string]CustomerNotice{}, RepeatQuietHours: 12}
}

var secretRe = regexp.MustCompile(`^[A-Za-z0-9]*$`)

func (w *Webhooks) Validate() error {
	if w.Customer == nil {
		w.Customer = map[string]CustomerNotice{}
	}
	if w.Secret != Mask && !secretRe.MatchString(w.Secret) {
		return errors.New("секрет: только латинские буквы и цифры, как в WEBHOOK_SECRET_HEADER панели")
	}
	if w.Enabled && (w.Secret == "" || (w.Secret != Mask && len(w.Secret) < 32)) {
		return errors.New("для приёма вебхуков нужен секрет от 32 символов")
	}
	if w.RepeatQuietHours < 0 || w.RepeatQuietHours > 168 {
		return errors.New("пауза между повторами: от 0 до 168 часов")
	}
	for k, n := range w.Customer {
		if len(n.Template) > 3500 {
			return fmt.Errorf("%s: шаблон длиннее 3500 символов", k)
		}
	}
	return nil
}
