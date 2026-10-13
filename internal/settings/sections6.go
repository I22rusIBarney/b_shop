package settings

import "errors"

// ---- geography of subscription requests ----

type Geo struct {
	// Enabled copies the panel's subscription request history and shows it on a map (Statistics → Geography).
	Enabled bool `json:"enabled"`
	// RetentionDays: older requests are deleted. IP addresses are personal data, so they are not kept forever.
	RetentionDays int `json:"retention_days"`
}

func defaultGeo() Geo { return Geo{Enabled: true, RetentionDays: 90} }

func (g *Geo) Validate() error {
	if g.RetentionDays < 7 || g.RetentionDays > 730 {
		return errors.New("срок хранения запросов: от 7 до 730 дней")
	}
	return nil
}
