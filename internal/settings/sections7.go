package settings

import "errors"

// ---- off-site backups ----

// Backups: every night the database and .env are packed, encrypted with Password and sent by the bot
// to the administrators from ADMIN_TELEGRAM_IDS. The recipients come only from .env, so someone who got into
// the admin panel cannot redirect the copies to themselves.
type Backups struct {
	Enabled bool `json:"enabled"`
	// Hour of the day (shop time zone) when the copy is made.
	Hour     int    `json:"hour"`
	Password string `json:"password" secret:"true"`
}

func defaultBackups() Backups { return Backups{Hour: 4} }

func (b *Backups) Validate() error {
	if b.Hour < 0 || b.Hour > 23 {
		return errors.New("час отправки: от 0 до 23")
	}
	if b.Enabled && len([]rune(b.Password)) < 10 {
		return errors.New("пароль копий — не короче 10 символов")
	}
	return nil
}
