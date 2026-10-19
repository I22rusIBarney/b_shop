package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bshop/internal/store"
)

// AdminNote is one rendered admin notification with its destination.
type AdminNote struct {
	Event      string
	Text       string // Telegram HTML
	ChatID     int64  // 0 = admins from ADMIN_TELEGRAM_IDS
	TopicID    int
	CustomerID int64
	ButtonText string
	ButtonPath string // web-app path, e.g. /admin/tickets/5
}

// EventInfo describes an admin notification type for the settings page.
type EventInfo struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Group    string   `json:"group"`
	Template string   `json:"template"`
	Vars     []string `json:"vars"`
}

// Common variables available in every template that has a customer; {time} is in every template.
var customerVars = []string{"user", "name", "username", "tg_id", "email", "customer_id", "time"}

// AdminEvents is the catalog of admin notifications. Templates are Telegram HTML; values are escaped.
var AdminEvents = []EventInfo{
	{"new_customer", "Новый пользователь", "Клиенты", "🆕 <b>Новый пользователь</b>\n\n👤 Пользователь: {user}{via}\n🕐 Время события: {time}", []string{"via"}},
	{"trial", "Активирован пробный период", "Клиенты", "⭐ <b>Активирован триал</b>\n\n👤 Пользователь: {user}\n🗓 Действует до: {expires} ({days} дн.)\n🕐 Время события: {time}", []string{"days", "plan", "expires"}},
	{"purchase", "Покупка", "Оплаты", "🛒 <b>{operation}</b>\n\n👤 Пользователь: {user}\n📦 Тариф: {plan} ({detail})\n📱 Устройства: {devices}\n💰 Сумма: {amount}{promo}\n💎 Провайдер: {provider}\n🗓 Действует до: {expires}\n🕐 Время события: {time}", []string{"operation", "plan", "detail", "devices", "amount", "promo", "provider", "expires"}},
	{"renew", "Продление", "Оплаты", "🔁 <b>{operation}</b>\n\n👤 Пользователь: {user}\n📦 Тариф: {plan} ({detail})\n📱 Устройства: {devices}\n💰 Сумма: {amount}{promo}\n💎 Провайдер: {provider}\n🗓 Действует до: {expires}\n🕐 Время события: {time}", []string{"operation", "plan", "detail", "devices", "amount", "promo", "provider", "expires"}},
	{"autopay", "Автопродление: успех", "Оплаты", "♻️ <b>Автопродление</b>\n\n👤 Пользователь: {user}\n📦 Тариф: {plan} ({detail})\n💰 Сумма: {amount}\n💎 Провайдер: {provider}\n🗓 Действует до: {expires}\n🕐 Время события: {time}", []string{"plan", "detail", "amount", "provider", "expires"}},
	{"autopay_error", "Автопродление: ошибка", "Оплаты", "⚠️ <b>Ошибка автоплатежа</b>\n\n👤 Пользователь: {user}\n📦 Тариф: {plan}\n💰 Сумма: {amount}\n❌ Причина: {error}\n🕐 Время события: {time}", []string{"plan", "amount", "error"}},
	{"plan_change", "Смена тарифа", "Оплаты", "🔄 <b>Смена тарифа</b>\n\n👤 {user}\n📦 {from} → {to}\n📊 {days}\n💰 {amount}\n🔄 {mode}\n🕐 Время события: {time}", []string{"mode", "from", "to", "days", "amount"}},
	{"refund", "Возврат", "Оплаты", "↩️ <b>Возврат</b>\n\n👤 Пользователь: {user}\n💰 Сумма: {amount}\n🧾 Платёж: {payment}\n🕐 Время события: {time}", []string{"amount", "payment"}},
	{"payment_error", "Ошибки оплаты", "Оплаты", "⚠️ <b>Ошибка оплаты</b>\n\n👤 Пользователь: {user}\n❌ {error}\n🕐 Время события: {time}", []string{"error"}},
	{"receipt_error", "Ошибки «Мой налог»", "Оплаты", "🧾 <b>Чек не отправлен в «Мой налог»</b>\n\n💳 Платёж: {payment} · {amount}\n❌ {error}\n🔁 Повторю автоматически\n🕐 Время события: {time}", []string{"payment", "amount", "error"}},
	{"promo", "Использован промокод", "Маркетинг", "🏷 <b>Использован промокод</b>\n\n👤 Пользователь: {user}\n🏷 Код: {code}\n🔓 Тип: {what}\n🕐 Время события: {time}", []string{"code", "what"}},
	{"referral_bonus", "Реферальный бонус", "Маркетинг", "💎 <b>Реферальный бонус</b>\n\n👤 Получатель: {user}\n🎁 Бонус: +{days} ({kind})\n🤝 За: {referral}\n🕐 Время: {time}", []string{"kind", "days", "referral"}},
	{"referral_invitee", "Бонус приглашённому", "Маркетинг", "💎 <b>Бонус приглашённому</b>\n\n👤 Получатель: {user}\n👥 Реферер: {referrer}\n🎁 Бонус: +{days} ({kind})\n🕐 Время: {time}", []string{"kind", "days", "referrer"}},
	{"broadcast_done", "Рассылка завершена", "Маркетинг", "📣 <b>Рассылка завершена</b>\n\n📝 {title}\n✅ Доставлено: {sent} из {total}\n❌ Ошибок: {failed}\n🕐 Время: {time}", []string{"title", "sent", "total", "failed"}},
	{"ticket_new", "Новое обращение", "Поддержка", "🆘 <b>Обращение #{ticket}</b>\n\n👤 {user}\n📝 {subject}\n\n{text}", []string{"ticket", "subject", "text"}},
	{"ticket_message", "Ответ клиента в обращении", "Поддержка", "💬 <b>Сообщение #{ticket}</b>\n\n👤 {user}\n📝 {subject}\n\n{text}", []string{"ticket", "subject", "text"}},
	{"ticket_escalated", "Ассистент передал оператору", "Поддержка", "🙋 <b>Обращение #{ticket} передано оператору</b>\n\n👤 {user}\n❓ {reason}", []string{"ticket", "reason"}},
	{"ai_draft", "Черновик ассистента", "Поддержка", "📝 <b>Черновик ответа к #{ticket}</b> (уверенность {confidence})\n\n👤 {user}\n\n{text}", []string{"ticket", "confidence", "text"}},
	{"kb_proposals", "Предложения в базу знаний", "Поддержка", "📚 Ассистент предложил статей для базы знаний: {count}. Проверьте в «Знания → Предложения»", []string{"count"}},
	{"ticket_rating", "Оценка поддержки", "Поддержка", "⭐ <b>Оценка {rating}/5</b> по обращению #{ticket}\n\n👤 {user}{comment}", []string{"ticket", "rating", "comment"}},
	{"system_error", "Системные ошибки", "Система", "🛠 <b>Системная ошибка</b>\n\n{error}\n🕐 Время: {time}", []string{"error"}},
	{"healthcheck", "Healthcheck: сбои и восстановление", "Система", "{report}", []string{"report"}},
	{"rw_node_down", "Нода недоступна", "Remnawave", "🔴 <b>Нода недоступна</b>\n\n📍 Нода: <b>{node}</b>\n🌐 Адрес: {address}\n🕐 Время: {time}\n{flag} Страна: {country}", []string{"node", "address", "country", "flag"}},
	{"rw_node_up", "Нода восстановлена", "Remnawave", "🟢 <b>Нода восстановлена</b>\n\n📍 Нода: <b>{node}</b>\n🌐 Адрес: {address}\n🕐 Время: {time}\n{flag} Страна: {country}", []string{"node", "address", "country", "flag"}},
	{"rw_node_changed", "Изменения нод", "Remnawave", "⚙️ <b>Нода {action}</b>\n\n📍 Нода: <b>{node}</b>\n🌐 Адрес: {address}\n🕐 Время: {time}", []string{"node", "address", "action"}},
	{"rw_node_traffic", "Трафик ноды", "Remnawave", "📶 <b>Трафик ноды</b>\n\n📍 Нода: <b>{node}</b>\n📊 Израсходовано: {used} из {limit}\n🕐 Время: {time}", []string{"node", "used", "limit"}},
	{"rw_torrent", "Торрент-трафик", "Remnawave", "🧲 <b>Торрент ({action})</b>\n\n📍 Нода: <b>{node}</b>\n👤 {user}\n🌐 IP: {ip} · на {duration}\n🎯 {destination}\n🕐 Время: {time}", []string{"node", "ip", "duration", "action", "destination", "rw_user"}},
	{"rw_login", "Вход в панель", "Remnawave", "✅ <b>Успешный вход</b>\n\n🕐 Время: {time}\n👤 Пользователь: {username}\n🌐 IP: {ip}\n📱 User-Agent: <code>{agent}</code>", []string{"username", "ip", "agent"}},
	{"rw_login_failed", "Неудачный вход в панель", "Remnawave", "🚫 <b>Неудачный вход</b>\n\n🕐 Время: {time}\n👤 Пользователь: {username}\n🌐 IP: {ip}\n❓ Причина: {reason}\n📱 User-Agent: <code>{agent}</code>", []string{"username", "ip", "agent", "reason"}},
	{"rw_panel_started", "Панель запущена", "Remnawave", "🚀 <b>Панель Remnawave запущена</b>\n\n🏷 Версия: {version}\n🕐 Время: {time}", []string{"version"}},
	{"rw_billing", "Оплата серверов", "Remnawave", "💳 <b>Оплата сервера</b>\n\n🏢 {provider} · {node}\n⏰ {when} ({date})", []string{"provider", "node", "when", "date", "url"}},
	{"rw_error", "Ошибки панели", "Remnawave", "⚠️ <b>Ошибка Remnawave</b>\n\n{error}\n🕐 Время: {time}", []string{"error"}},
}

func eventInfo(key string) (EventInfo, bool) {
	for _, e := range AdminEvents {
		if e.Key == key {
			return e, true
		}
	}
	return EventInfo{}, false
}

var varRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// renderVars replaces {name} with escaped values; unknown names stay as they are.
func renderVars(tpl string, vars map[string]string, escapeValues bool) string {
	return varRe.ReplaceAllStringFunc(tpl, func(m string) string {
		v, ok := vars[m[1:len(m)-1]]
		if !ok {
			return m
		}
		if escapeValues {
			return escape(v)
		}
		return v
	})
}

func customerVarMap(c *store.Customer) map[string]string {
	v := map[string]string{"name": c.DisplayName(), "customer_id": strconv.FormatInt(c.ID, 10)}
	if c.Username != nil {
		v["username"] = "@" + *c.Username
	}
	if c.TelegramID != nil {
		v["tg_id"] = strconv.FormatInt(*c.TelegramID, 10)
	}
	if c.Email != nil {
		v["email"] = *c.Email
	}
	return v
}

// userHTML is the customer line of the old bot: Telegram ID as a profile link, name and @username;
// e-mail customers show the address, others the shop id.
func userHTML(c *store.Customer) string {
	name := c.DisplayName()
	var b strings.Builder
	switch {
	case c.TelegramID != nil:
		fmt.Fprintf(&b, `<a href="tg://user?id=%d">%d</a>`, *c.TelegramID, *c.TelegramID)
	case c.Email != nil:
		b.WriteString(escape(*c.Email))
	default:
		fmt.Fprintf(&b, "#%d", c.ID)
	}
	at := ""
	if c.Username != nil && *c.Username != "" {
		at = "@" + *c.Username
	}
	if name != "" && name != at && (c.Email == nil || name != *c.Email || c.TelegramID != nil) {
		b.WriteString(" (" + escape(name) + ")")
	}
	if at != "" {
		b.WriteString(" " + escape(at))
	}
	return b.String()
}

// flag turns an ISO country code into its flag emoji (🏳 when unknown).
func flag(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return "🏳"
	}
	return string(rune(0x1F1E6+int(cc[0]-'A'))) + string(rune(0x1F1E6+int(cc[1]-'A')))
}

// optionalVars are pre-built tails ("\n🎟 …") that are simply left out when absent.
var optionalVars = map[string]bool{"promo": true, "via": true, "comment": true}

// Raw marks a value that is already Telegram HTML and must not be escaped (e.g. pre-built optional lines).
type Raw string

// event sends an admin notification built from the catalog template (or the admin's own template).
// vars values are plain text (escaped when rendered) unless they are Raw.
func (s *Service) event(ctx context.Context, key string, c *store.Customer, vars map[string]any) {
	s.eventLink(ctx, key, c, vars, "", "")
}

func (s *Service) eventLink(ctx context.Context, key string, c *store.Customer, vars map[string]any, buttonText, buttonPath string) {
	info, ok := eventInfo(key)
	if !ok {
		info = EventInfo{Key: key, Template: "{text}"}
	}
	chatID, topicID, tpl, enabled := s.set().Notifications.Route(key)
	if !enabled {
		return
	}
	if strings.TrimSpace(tpl) == "" {
		tpl = info.Template
	}
	plain := map[string]string{"time": s.stamp(time.Now())}
	raw := map[string]string{}
	var customerID int64
	if c != nil {
		customerID = c.ID
		for k, v := range customerVarMap(c) {
			plain[k] = v
		}
		raw["user"] = userHTML(c)
	}
	for k, v := range vars {
		switch x := v.(type) {
		case Raw:
			raw[k] = string(x)
		case string:
			plain[k] = s.stripSecrets(x) // error texts of external APIs end up here
			delete(raw, k)
		default:
			plain[k] = fmt.Sprint(x)
			delete(raw, k)
		}
	}
	for _, v := range info.Vars { // a caller that has no value for a field: a dash, not a raw "{field}"
		if _, ok := plain[v]; !ok {
			if _, ok := raw[v]; !ok {
				plain[v] = "—"
				if optionalVars[v] {
					plain[v] = ""
				}
			}
		}
	}
	text := renderVars(tpl, plain, true)
	text = renderVars(text, raw, false)
	if buttonPath == "" && customerID > 0 {
		buttonText, buttonPath = "Открыть профиль", fmt.Sprintf("/admin/customers/%d", customerID)
	}
	s.Notify.Admin(ctx, AdminNote{Event: key, Text: text, ChatID: chatID, TopicID: topicID, CustomerID: customerID,
		ButtonText: buttonText, ButtonPath: buttonPath})
}

// eventByID is event() for code paths that only have the customer id.
func (s *Service) eventByID(ctx context.Context, key string, customerID int64, vars map[string]any) {
	var c *store.Customer
	if customerID > 0 {
		c, _ = store.GetCustomer(ctx, s.Store.Pool, customerID)
	}
	s.event(ctx, key, c, vars)
}

// TestAdminEvent sends a sample of an event to its configured destination.
func (s *Service) TestAdminEvent(ctx context.Context, key string, admin *store.Customer) error {
	info, ok := eventInfo(key)
	if !ok {
		return userErr("неизвестное событие")
	}
	if _, _, _, on := s.set().Notifications.Route(key); !on {
		return userErr("это уведомление выключено")
	}
	vars := map[string]any{}
	for _, v := range info.Vars {
		vars[v] = "‹" + v + "›"
	}
	vars["promo"] = Raw("")
	vars["via"] = ""
	vars["comment"] = Raw("")
	vars["flag"] = flag("RU")
	delete(vars, "report")
	if key == "healthcheck" {
		vars["report"] = Raw("✅ <b>Healthcheck</b> — 3/3 в работе\n🕐 <i>Проверка: " + s.stamp(time.Now()) + "</i>\n\n<b>Изменения:</b>\n" +
			"✅ <b>smtp</b> — восстановлен (325ms) · был недоступен 1 мин 5 с (с 03:50:25)\n\n✅ postgres (&lt;1ms)\n✅ remnawave (4ms)\n✅ smtp (325ms)\n\n<i>Это тестовое сообщение</i>")
	}
	s.eventLink(ctx, key, admin, vars, "", "")
	return nil
}

// truncate shortens text for notifications.
func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
