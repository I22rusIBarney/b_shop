package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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

// Common variables available in every template that has a customer.
var customerVars = []string{"user", "name", "username", "tg_id", "email", "customer_id"}

// AdminEvents is the catalog of admin notifications. Templates are Telegram HTML; values are escaped.
var AdminEvents = []EventInfo{
	{"new_customer", "Новый пользователь", "Клиенты", "🆕 Новый пользователь: {user}{via}", []string{"via"}},
	{"trial", "Активирован пробный период", "Клиенты", "⭐ Активирован триал\n👤 {user} · {days} дн.", []string{"days", "plan"}},
	{"purchase", "Покупка", "Оплаты", "🛒 {operation}\n👤 {user}\n📦 {plan} · {detail}\n💰 {amount}{promo}", []string{"operation", "plan", "detail", "amount", "promo"}},
	{"renew", "Продление", "Оплаты", "🔁 {operation}\n👤 {user}\n📦 {plan} · {detail}\n💰 {amount}{promo}", []string{"operation", "plan", "detail", "amount", "promo"}},
	{"autopay", "Автопродление: успех", "Оплаты", "♻️ Автопродление\n👤 {user}\n📦 {plan} · {detail}\n💰 {amount}", []string{"plan", "detail", "amount"}},
	{"autopay_error", "Автопродление: ошибка", "Оплаты", "⚠️ Ошибка автоплатежа\n👤 {user}\n📦 {plan} · {amount}\n❌ {error}", []string{"plan", "amount", "error"}},
	{"plan_change", "Смена тарифа", "Оплаты", "🔄 Смена тарифа ({mode})\n👤 {user}\n📦 {from} → {to}\n📅 {days}{amount}", []string{"mode", "from", "to", "days", "amount"}},
	{"refund", "Возврат", "Оплаты", "↩️ Возврат {amount} по платежу {payment}\n👤 {user}", []string{"amount", "payment"}},
	{"payment_error", "Ошибки оплаты", "Оплаты", "⚠️ {error}\n👤 {user}", []string{"error"}},
	{"receipt_error", "Ошибки «Мой налог»", "Оплаты", "🧾 Не удалось отправить чек в «Мой налог» (платёж {payment}, {amount}): {error}. Повторю автоматически.", []string{"payment", "amount", "error"}},
	{"promo", "Использован промокод", "Маркетинг", "🎟 Промокод <b>{code}</b> ({what})\n👤 {user}", []string{"code", "what"}},
	{"referral_bonus", "Реферальный бонус", "Маркетинг", "🎁 Реферальный бонус {kind}: +{days}\n👤 Получатель: {user}\n🤝 За: {referral}", []string{"kind", "days", "referral"}},
	{"broadcast_done", "Рассылка завершена", "Маркетинг", "📣 Рассылка «{title}» завершена: доставлено {sent} из {total}, ошибок {failed}", []string{"title", "sent", "total", "failed"}},
	{"ticket_new", "Новое обращение", "Поддержка", "🆘 Новое обращение #{ticket}\n👤 {user}\n💬 {text}", []string{"ticket", "subject", "text"}},
	{"ticket_message", "Ответ клиента в обращении", "Поддержка", "💬 Обращение #{ticket}\n👤 {user}\n{text}", []string{"ticket", "text"}},
	{"ticket_escalated", "Ассистент передал оператору", "Поддержка", "🙋 Обращение #{ticket} передано оператору\n👤 {user}\n❓ {reason}", []string{"ticket", "reason"}},
	{"ai_draft", "Черновик ассистента", "Поддержка", "📝 Черновик ответа к #{ticket} (уверенность {confidence})\n👤 {user}\n{text}", []string{"ticket", "confidence", "text"}},
	{"ticket_rating", "Оценка поддержки", "Поддержка", "⭐ Оценка {rating}/5 по обращению #{ticket}\n👤 {user}{comment}", []string{"ticket", "rating", "comment"}},
	{"system_error", "Системные ошибки", "Система", "🛠 {error}", []string{"error"}},
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
	v := map[string]string{"user": whoLine(c), "name": c.DisplayName(), "customer_id": strconv.FormatInt(c.ID, 10)}
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
	plain := map[string]string{}
	raw := map[string]string{}
	var customerID int64
	if c != nil {
		customerID = c.ID
		for k, v := range customerVarMap(c) {
			plain[k] = v
		}
	}
	for k, v := range vars {
		switch x := v.(type) {
		case Raw:
			raw[k] = string(x)
		case string:
			plain[k] = x
		default:
			plain[k] = fmt.Sprint(x)
		}
	}
	text := renderVars(tpl, plain, true)
	text = renderVars(text, raw, false)
	if buttonPath == "" && customerID > 0 {
		buttonText, buttonPath = "Открыть клиента", fmt.Sprintf("/admin/customers/%d", customerID)
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
