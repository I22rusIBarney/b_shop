package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bshop/internal/moynalog"
	"bshop/internal/store"
	"bshop/internal/yookassa"
)

var resetStrategies = map[string]bool{"NO_RESET": true, "DAY": true, "WEEK": true, "MONTH": true, "MONTH_ROLLING": true}

// ValidatePlan normalises and checks a plan coming from the admin UI.
func ValidatePlan(p *store.Plan) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return userErr("укажите название тарифа")
	}
	if p.Kind == "" {
		p.Kind = "period"
	}
	if p.Kind != "period" && p.Kind != "traffic" {
		return userErr("неизвестный тип тарифа")
	}
	if p.TrafficResetStrategy == "" {
		p.TrafficResetStrategy = "MONTH"
	}
	if !resetStrategies[p.TrafficResetStrategy] {
		return userErr("неизвестная стратегия сброса трафика")
	}
	if p.TrafficLimitGB < 0 || p.DeviceLimit < 0 || p.MaxPerCustomer < 0 || p.ExtraDevicePrice < 0 || p.MaxExtraDevices < 0 {
		return userErr("лимиты не могут быть отрицательными")
	}
	p.Notice = strings.TrimSpace(p.Notice)
	if len([]rune(p.Notice)) > 1000 {
		return userErr("«Важно знать перед покупкой» — до 1000 символов")
	}
	if p.TrafficResetPrice < 0 {
		return userErr("цена сброса трафика не может быть отрицательной")
	}
	for i, pk := range p.TrafficPackages {
		if pk.GB <= 0 || pk.Price <= 0 {
			return userErr(fmt.Sprintf("пакет трафика %d: укажите объём и цену больше нуля", i+1))
		}
	}
	if p.TrafficTopupEnabled && len(p.TrafficPackages) == 0 {
		return userErr("добавьте хотя бы один пакет трафика или выключите докупку")
	}
	p.RWTag = strings.ToUpper(strings.TrimSpace(p.RWTag))
	if p.RWTag != "" {
		if len(p.RWTag) > 16 {
			return userErr("тег Remnawave — до 16 символов")
		}
		for _, r := range p.RWTag {
			if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
				return userErr("тег Remnawave: только латиница A-Z, цифры и _")
			}
		}
	}
	if p.RequiredPlanID != nil && p.ID != 0 && *p.RequiredPlanID == p.ID {
		return userErr("тариф не может требовать сам себя")
	}
	if !p.IsTrial && len(p.Periods) == 0 {
		return userErr("добавьте хотя бы один период с ценой")
	}
	seen := map[string]bool{}
	for i := range p.Periods {
		per := &p.Periods[i]
		if per.Unit == "" {
			per.Unit = "month"
		}
		if per.Unit != "day" && per.Unit != "week" && per.Unit != "month" {
			return userErr("период: единица должна быть день/неделя/месяц")
		}
		if per.Amount <= 0 {
			return userErr("период должен быть больше нуля")
		}
		if per.Price < 0 {
			return userErr("цена не может быть отрицательной")
		}
		k := fmt.Sprintf("%s%d", per.Unit, per.Amount)
		if seen[k] {
			return userErr("периоды повторяются")
		}
		seen[k] = true
	}
	for i := range p.Features {
		p.Features[i] = strings.TrimSpace(p.Features[i])
	}
	return nil
}

// TestIntegration checks credentials of a configured integration and returns a human message.
func (s *Service) TestIntegration(ctx context.Context, section string, admin *store.Customer) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st := s.set()
	switch section {
	case "yookassa":
		y := st.YooKassa
		if y.ShopID == "" || y.SecretKey == "" {
			return "", userErr("укажите Shop ID и секретный ключ")
		}
		if err := yookassa.New(y.APIURL, y.ShopID, y.SecretKey).Ping(ctx); err != nil {
			return "", err
		}
		return "ЮKassa: ключи работают", nil
	case "moynalog":
		m := st.MoyNalog
		if m.Login == "" || m.Password == "" {
			return "", userErr("укажите логин и пароль")
		}
		if err := moynalog.New(m.APIURL, m.Login, m.Password).Check(ctx); err != nil {
			return "", err
		}
		return "«Мой налог»: вход выполнен", nil
	case "smtp":
		if admin.Email == nil {
			return "", userErr("привяжите почту к своему аккаунту, чтобы получить тестовое письмо")
		}
		if err := s.Mailer.Send(ctx, *admin.Email, "Тестовое письмо "+st.General.ShopName,
			"<p>Почта настроена правильно.</p>"); err != nil {
			return "", err
		}
		return "Письмо отправлено на " + *admin.Email, nil
	case "ai":
		return s.TestAI(ctx)
	case "backups":
		cancel() // a dump can take longer than the 30 s test budget
		return s.MakeBackup(context.WithoutCancel(ctx), true)
	case "health":
		cancel() // a round may take two timeouts plus the retry pause
		hc := s.set().Health
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(2*hc.TimeoutSec+5)*time.Second)
		defer rcancel()
		if !s.RunHealth(rctx) {
			return "", userErr("проверка уже идёт, подождите несколько секунд")
		}
		r := s.HealthStatus()
		if r.Up == r.Total {
			return fmt.Sprintf("Всё работает: %d из %d", r.Up, r.Total), nil
		}
		var bad []string
		for _, c := range r.Components {
			if !c.Up {
				bad = append(bad, c.Name)
			}
		}
		return "", fmt.Errorf("в работе %d из %d, ошибки: %s", r.Up, r.Total, strings.Join(bad, ", "))
	case "remnawave":
		sq, err := s.Panel.InternalSquads(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Remnawave отвечает, сквадов: %d", len(sq)), nil
	}
	return "", userErr("для этого раздела нет проверки")
}
