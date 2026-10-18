package service

import (
	"context"
	"fmt"
	"html"
	"os"
	"time"

	"bshop/internal/backup"
	"bshop/internal/store"
)

// FileSender sends a document to a Telegram chat (the bot); nil without a bot token.
type FileSender interface {
	SendFile(ctx context.Context, chatID int64, name string, data []byte, caption string) error
}

// Telegram bots may upload documents up to 50 MB.
const backupMaxSize = 49 << 20

// RunBackups makes the nightly copy once a day, at the configured hour or later if the server was down then.
func (s *Service) RunBackups(ctx context.Context) {
	b := s.set().Backups
	if !b.Enabled || b.Password == "" {
		return
	}
	loc := s.set().General.Location()
	now := time.Now().In(loc)
	if now.Hour() < b.Hour {
		return
	}
	due := time.Date(now.Year(), now.Month(), now.Day(), b.Hour, 0, 0, 0, loc)
	last, err := store.LastBackupAttempt(ctx, s.Store.Pool)
	if err != nil || !last.Before(due) {
		return // one automatic attempt a day; a failure is reported to the admins, not retried every 10 minutes
	}
	if _, err := s.MakeBackup(ctx, false); err != nil {
		s.Log.Warn("backup", "err", err)
	}
}

// MakeBackup dumps the database, encrypts it with the backup password and sends it to every administrator
// from ADMIN_TELEGRAM_IDS. Returns a short report for the admin panel.
func (s *Service) MakeBackup(ctx context.Context, manual bool) (string, error) {
	defer s.lock("backup")()
	b := s.set().Backups
	if b.Password == "" {
		return "", userErr("задайте пароль копий и сохраните настройки")
	}
	if s.Files == nil {
		return "", userErr("бот не подключён — копию некуда отправить")
	}
	if len(s.Cfg.AdminTelegram) == 0 {
		return "", userErr("в .env не указаны ADMIN_TELEGRAM_IDS — копию некому отправить")
	}
	id, err := store.StartBackupRun(ctx, s.Store.Pool, manual)
	if err != nil {
		return "", err
	}
	size, sent, err := s.sendBackup(ctx, b.Password)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	_ = store.FinishBackupRun(context.WithoutCancel(ctx), s.Store.Pool, id, err == nil, size, sent, errText)
	if err != nil {
		s.Notify.Admin(ctx, AdminNote{Event: "backup_failed", Text: "⚠️ <b>Резервная копия не сделана</b>\n" + html.EscapeString(errText) +
			"\n\nПроверьте: Настройки → Резервные копии.", ButtonText: "Открыть", ButtonPath: "/admin/settings/backups"})
		return "", err
	}
	return fmt.Sprintf("Копия отправлена (%s) — %d адм.", humanSize(size), sent), nil
}

func (s *Service) sendBackup(ctx context.Context, password string) (int64, int, error) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	dump, err := backup.Dump(dctx, s.Cfg.DatabaseURL)
	if err != nil {
		return 0, 0, err
	}
	now := time.Now().In(s.set().General.Location())
	arc, err := backup.Archive(dump, backup.EnvFile(os.Environ()), now)
	if err != nil {
		return 0, 0, err
	}
	file, err := backup.Encrypt(password, arc)
	if err != nil {
		return 0, 0, err
	}
	size := int64(len(file))
	if size > backupMaxSize {
		return size, 0, fmt.Errorf("копия %s больше 49 МБ — Telegram такую не примет; локальные копии на сервере делаются по-прежнему (bshop backup)", humanSize(size))
	}
	name := "bobrgo-" + now.Format("2006-01-02-1504") + backup.Ext
	caption := fmt.Sprintf("🔐 Резервная копия %s · %s\nЗашифрована паролем из настроек. Восстановление: bshop restore %s",
		html.EscapeString(s.set().General.ShopName), now.Format("02.01.2006 15:04"), name)
	sent := 0
	var lastErr error
	for _, chat := range s.Cfg.AdminTelegram {
		if err := s.Files.SendFile(ctx, chat, name, file, caption); err != nil {
			lastErr = err
			s.Log.Warn("backup send", "chat", chat, "err", err)
			continue
		}
		sent++
	}
	if sent == 0 {
		return size, 0, fmt.Errorf("не удалось отправить в Telegram: %v (администратор должен хотя бы раз написать боту)", lastErr)
	}
	return size, sent, nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d КБ", n>>10)
	}
	return fmt.Sprintf("%d Б", n)
}
