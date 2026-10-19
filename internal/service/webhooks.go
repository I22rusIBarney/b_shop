package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"bshop/internal/settings"
	"bshop/internal/store"

	"github.com/jackc/pgx/v5"
)

// Remnawave webhooks (panel → shop). The panel signs the raw body with HMAC-SHA256 using
// WEBHOOK_SECRET_HEADER and sends the hex digest in X-Remnawave-Signature.
// User events become customer messages (templates in the admin), node/service/torrent/billing
// events become admin notifications routed like every other admin event.

// CustomerWebhookEvent describes a customer message for a panel event.
type CustomerWebhookEvent struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Hint     string   `json:"hint,omitempty"`
	Template string   `json:"template"`
	Vars     []string `json:"vars"`
	Default  bool     `json:"default"` // enabled until the admin decides otherwise
	Button   string   `json:"button,omitempty"`
	Repeats  bool     `json:"-"` // can fire repeatedly: obey the repeat pause
}

var subVars = []string{"name", "plan", "expire_date", "time_left", "used", "limit"}

// CustomerWebhookEvents is the catalog of customer messages. Templates are Telegram HTML; values are escaped.
var CustomerWebhookEvents = []CustomerWebhookEvent{
	{"user.expiration.before", "Подписка скоро закончится", "Когда — задаётся в панели (EXPIRATION_NOTIFICATIONS). Обычно хватает автоматизации «Скоро истекает», поэтому выключено",
		"⏳ Подписка <b>{plan}</b> закончится {expire_date} — осталось {time_left}.\nПродлите заранее, чтобы VPN не отключился.", subVars, false, "Продлить", true},
	{"user.expired", "Подписка закончилась", "",
		"⌛ Подписка <b>{plan}</b> закончилась, VPN отключён.\nПродлите её в пару нажатий — все настройки сохранятся.", subVars, true, "Продлить", false},
	{"user.expiration.after", "Напоминание после окончания", "Через сколько часов после окончания — задаётся в панели",
		"👋 Подписка <b>{plan}</b> закончилась {expire_date}. Вернуть доступ можно в любой момент.", subVars, false, "Продлить", true},
	{"user.bandwidth_usage_threshold_reached", "Израсходована часть трафика", "Пороги (например 80 и 95%) задаются в панели",
		"📶 По подписке <b>{plan}</b> израсходовано {percent}% трафика: {used} из {limit}.", append([]string{"percent"}, subVars...), true, "Открыть кабинет", true},
	{"user.limited", "Трафик закончился", "",
		"🚫 Трафик по подписке <b>{plan}</b> закончился ({used} из {limit}). Докупите трафик или дождитесь сброса.", subVars, true, "Открыть кабинет", true},
	{"user.traffic_reset", "Трафик сброшен", "",
		"🔄 Трафик по подписке <b>{plan}</b> обновился — снова доступно {limit}.", subVars, false, "", false},
	{"user.first_connected", "Первое подключение", "",
		"🎉 Отлично, VPN подключён! Если что-то не работает — напишите в поддержку прямо из кабинета.", subVars, true, "", false},
	{"user.not_connected", "Не подключился после покупки", "Включается в панели (NOT_CONNECTED_USERS_NOTIFICATIONS)",
		"🤔 Подписка <b>{plan}</b> оформлена, но вы ещё не подключились. Инструкция по установке — в кабинете, а если не получается — поможем в поддержке.",
		append([]string{"hours"}, subVars...), true, "Как подключиться", true},
	{"user.disabled", "Подписка отключена", "Срабатывает и когда отключаете вы сами",
		"⛔ Подписка <b>{plan}</b> отключена. Если это ошибка — напишите в поддержку.", subVars, false, "", false},
	{"user.enabled", "Подписка снова включена", "",
		"✅ Подписка <b>{plan}</b> снова работает.", subVars, false, "", false},
	{"user.revoked", "Ссылка подписки перевыпущена", "",
		"🔑 Ссылка подписки <b>{plan}</b> обновлена. Добавьте новую ссылку из кабинета в приложение — старая больше не работает.", subVars, false, "Открыть кабинет", false},
	{"user_hwid_devices.added", "Новое устройство", "",
		"📱 К подписке <b>{plan}</b> подключено новое устройство: {device}.\nЕсли это не вы — удалите его в кабинете в разделе «Устройства».",
		append([]string{"device", "platform"}, subVars...), true, "Устройства", false},
	{"user_hwid_devices.deleted", "Устройство удалено", "",
		"🗑 Устройство {device} отключено от подписки <b>{plan}</b>.", append([]string{"device", "platform"}, subVars...), false, "", false},
	{"torrent_blocker.report", "Заблокирован торрент", "Нужен Torrent Blocker на нодах (панель 2.7+)",
		"🧲 На сервере {server} замечен торрент-трафик с вашего подключения ({ip}). Доступ с этого IP ограничен на {duration}.\nТорренты через VPN запрещены правилами сервиса.",
		append([]string{"ip", "server", "duration", "action"}, subVars...), true, "", false},
}

func customerWebhookEvent(key string) (CustomerWebhookEvent, bool) {
	for _, e := range CustomerWebhookEvents {
		if e.Key == key {
			return e, true
		}
	}
	return CustomerWebhookEvent{}, false
}

// ErrBadSignature means the payload is not from our panel (wrong or missing secret).
var ErrBadSignature = errors.New("bad webhook signature")

// ErrWebhooksOff means webhooks are disabled in the admin.
var ErrWebhooksOff = errors.New("webhooks disabled")

type rwWebhook struct {
	Scope     string          `json:"scope"`
	Event     string          `json:"event"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
	Meta      *struct {
		NotConnectedAfterHours *float64 `json:"notConnectedAfterHours"`
		Expiration             *float64 `json:"expiration"`
	} `json:"meta"`
}

type rwUser struct {
	ID                int64     `json:"id"`
	Username          string    `json:"username"`
	Status            string    `json:"status"`
	TrafficLimitBytes int64     `json:"trafficLimitBytes"`
	ExpireAt          time.Time `json:"expireAt"`
	TelegramID        *int64    `json:"telegramId"`
	LastThreshold     int       `json:"lastTriggeredThreshold"`
	UserTraffic       struct {
		UsedTrafficBytes int64 `json:"usedTrafficBytes"`
	} `json:"userTraffic"`
}

type rwNode struct {
	Name          string `json:"name"`
	Address       string `json:"address"`
	Port          *int   `json:"port"`
	CountryCode   string `json:"countryCode"`
	TrafficLimit  *int64 `json:"trafficLimitBytes"`
	TrafficUsed   *int64 `json:"trafficUsedBytes"`
	NotifyPercent *int   `json:"notifyPercent"`
}

type rwDevice struct {
	HWID        string  `json:"hwid"`
	Platform    *string `json:"platform"`
	OSVersion   *string `json:"osVersion"`
	DeviceModel *string `json:"deviceModel"`
	UserAgent   *string `json:"userAgent"`
}

// VerifyRemnawaveSignature checks the HMAC of the raw body.
func (s *Service) VerifyRemnawaveSignature(body []byte, signature string) error {
	w := s.set().Webhooks
	if !w.Enabled {
		return ErrWebhooksOff
	}
	if w.Secret == "" {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(w.Secret))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || !hmac.Equal(want, got) {
		return ErrBadSignature
	}
	return nil
}

// HandleRemnawaveWebhook processes one verified payload. Retries of the same body are ignored.
func (s *Service) HandleRemnawaveWebhook(ctx context.Context, body []byte) error {
	var ev rwWebhook
	if err := json.Unmarshal(body, &ev); err != nil {
		return userErr("неверный JSON")
	}
	if ev.Event == "" {
		return userErr("нет поля event")
	}
	sum := sha256.Sum256(body)
	var id int64
	err := s.Store.Pool.QueryRow(ctx, `INSERT INTO webhook_events (event, body_hash) VALUES ($1, $2)
		ON CONFLICT (source, body_hash) DO NOTHING RETURNING id`, ev.Event, sum[:]).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // duplicate delivery
		}
		return err
	}
	customerID, summary, result := s.dispatchWebhook(ctx, &ev)
	_, err = s.Store.Pool.Exec(ctx, `UPDATE webhook_events SET customer_id=$2, summary=$3, result=$4 WHERE id=$1`,
		id, nullID(customerID), truncate(summary, 300), result)
	return err
}

func nullID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// dispatchWebhook acts on the event and says what it did, for the journal.
func (s *Service) dispatchWebhook(ctx context.Context, ev *rwWebhook) (customerID int64, summary, result string) {
	switch ev.Scope {
	case "user":
		var u rwUser
		if err := json.Unmarshal(ev.Data, &u); err != nil {
			return 0, "", "ошибка разбора: " + err.Error()
		}
		key := ev.Event
		vars := map[string]string{}
		if ev.Event == "user.expiration" {
			key = "user.expiration.before"
			if ev.Meta != nil && ev.Meta.Expiration != nil && *ev.Meta.Expiration > 0 {
				key = "user.expiration.after"
			}
		}
		if ev.Event == "user.not_connected" && ev.Meta != nil && ev.Meta.NotConnectedAfterHours != nil {
			vars["hours"] = fmt.Sprint(int(*ev.Meta.NotConnectedAfterHours))
		}
		if ev.Event == "user.bandwidth_usage_threshold_reached" {
			vars["percent"] = fmt.Sprint(u.LastThreshold)
			if u.LastThreshold == 0 && u.TrafficLimitBytes > 0 {
				vars["percent"] = fmt.Sprint(int(math.Round(float64(u.UserTraffic.UsedTrafficBytes) / float64(u.TrafficLimitBytes) * 100)))
			}
		}
		return s.customerWebhook(ctx, key, &u, vars, "")
	case "user_hwid_devices":
		var d struct {
			User   rwUser   `json:"user"`
			Device rwDevice `json:"hwidUserDevice"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return 0, "", "ошибка разбора: " + err.Error()
		}
		name := deviceTitle(d.Device)
		vars := map[string]string{"device": name, "platform": deref(d.Device.Platform)}
		cid, sum, res := s.customerWebhook(ctx, ev.Event, &d.User, vars, "/devices")
		if cid > 0 {
			kind := "device_added"
			if ev.Event == "user_hwid_devices.deleted" {
				kind = "device_removed"
			}
			_ = store.AddEvent(ctx, s.Store.Pool, cid, kind, map[string]any{"device": name, "hwid": maskHWID(d.Device.HWID)}, nil)
		}
		return cid, sum, res
	case "torrent_blocker":
		var d struct {
			Node   rwNode `json:"node"`
			User   rwUser `json:"user"`
			Report struct {
				Action struct {
					Blocked       bool    `json:"blocked"`
					IP            string  `json:"ip"`
					BlockDuration float64 `json:"blockDuration"`
				} `json:"actionReport"`
				Xray struct {
					Destination string `json:"destination"`
				} `json:"xrayReport"`
			} `json:"report"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return 0, "", "ошибка разбора: " + err.Error()
		}
		action := "предупреждение"
		if d.Report.Action.Blocked {
			action = "блокировка"
		}
		vars := map[string]string{"ip": d.Report.Action.IP, "server": d.Node.Name, "duration": humanSeconds(d.Report.Action.BlockDuration), "action": action}
		cid, sum, res := s.customerWebhook(ctx, "torrent_blocker.report", &d.User, vars, "")
		adminVars := map[string]any{"node": d.Node.Name, "ip": d.Report.Action.IP, "duration": vars["duration"], "action": action,
			"destination": d.Report.Xray.Destination, "rw_user": d.User.Username}
		if cid > 0 {
			s.eventByID(ctx, "rw_torrent", cid, adminVars)
			_ = store.AddEvent(ctx, s.Store.Pool, cid, "torrent", map[string]any{"server": d.Node.Name, "ip": d.Report.Action.IP}, nil)
		} else {
			adminVars["user"] = d.User.Username
			s.event(ctx, "rw_torrent", nil, adminVars)
		}
		return cid, sum, res + " · админам"
	case "node":
		var n rwNode
		if err := json.Unmarshal(ev.Data, &n); err != nil {
			return 0, "", "ошибка разбора: " + err.Error()
		}
		addr := n.Address
		if n.Port != nil && *n.Port > 0 {
			addr = net.JoinHostPort(n.Address, strconv.Itoa(*n.Port))
		}
		vars := map[string]any{"node": n.Name, "address": addr, "country": n.CountryCode, "flag": flag(n.CountryCode)}
		summary = n.Name
		switch ev.Event {
		case "node.connection_lost":
			s.event(ctx, "rw_node_down", nil, vars)
		case "node.connection_restored":
			s.event(ctx, "rw_node_up", nil, vars)
		case "node.traffic_notify":
			vars["used"] = humanBytes(deref64(n.TrafficUsed))
			vars["limit"] = humanBytes(deref64(n.TrafficLimit))
			s.event(ctx, "rw_node_traffic", nil, vars)
		default:
			vars["action"] = map[string]string{"node.created": "добавлена", "node.modified": "изменена", "node.disabled": "выключена",
				"node.enabled": "включена", "node.deleted": "удалена"}[ev.Event]
			if vars["action"] == "" {
				vars["action"] = ev.Event
			}
			s.event(ctx, "rw_node_changed", nil, vars)
		}
		return 0, summary, "админам"
	case "service":
		var d struct {
			Login *struct {
				Username    string `json:"username"`
				IP          string `json:"ip"`
				UserAgent   string `json:"userAgent"`
				Description string `json:"description"`
			} `json:"loginAttempt"`
			PanelVersion string `json:"panelVersion"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return 0, "", "ошибка разбора: " + err.Error()
		}
		switch ev.Event {
		case "service.panel_started":
			s.event(ctx, "rw_panel_started", nil, map[string]any{"version": d.PanelVersion})
			return 0, "версия " + d.PanelVersion, "админам"
		case "service.login_attempt_success", "service.login_attempt_failed":
			if d.Login == nil {
				return 0, "", "нет данных о входе"
			}
			key := "rw_login"
			if ev.Event == "service.login_attempt_failed" {
				key = "rw_login_failed"
			}
			s.event(ctx, key, nil, map[string]any{"username": d.Login.Username, "ip": d.Login.IP,
				"agent": truncate(d.Login.UserAgent, 120), "reason": d.Login.Description})
			return 0, d.Login.Username + " · " + d.Login.IP, "админам"
		}
		return 0, "", "пропущено"
	case "crm":
		var d struct {
			Provider string    `json:"providerName"`
			Node     string    `json:"nodeName"`
			Next     time.Time `json:"nextBillingAt"`
			URL      string    `json:"loginUrl"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return 0, "", "ошибка разбора: " + err.Error()
		}
		when := map[string]string{
			"crm.infra_billing_node_payment_in_7_days":      "оплата через 7 дней",
			"crm.infra_billing_node_payment_in_48hrs":       "оплата через 2 дня",
			"crm.infra_billing_node_payment_in_24hrs":       "оплата завтра",
			"crm.infra_billing_node_payment_due_today":      "оплата сегодня",
			"crm.infra_billing_node_payment_overdue_24hrs":  "оплата просрочена на сутки",
			"crm.infra_billing_node_payment_overdue_48hrs":  "оплата просрочена на 2 дня",
			"crm.infra_billing_node_payment_overdue_7_days": "оплата просрочена на неделю",
		}[ev.Event]
		s.event(ctx, "rw_billing", nil, map[string]any{"provider": d.Provider, "node": d.Node, "when": when,
			"date": d.Next.In(s.set().General.Location()).Format("02.01.2006"), "url": d.URL})
		return 0, d.Provider + " · " + d.Node, "админам"
	case "errors":
		var d struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		s.event(ctx, "rw_error", nil, map[string]any{"error": d.Description})
		return 0, d.Description, "админам"
	}
	return 0, "", "пропущено: неизвестный тип"
}

// customerWebhook sends the customer message for key, if enabled, and says what happened.
func (s *Service) customerWebhook(ctx context.Context, key string, u *rwUser, vars map[string]string, path string) (int64, string, string) {
	summary := u.Username
	sub, err := store.GetSubscriptionByRWUser(ctx, s.Store.Pool, u.ID)
	if err != nil {
		return 0, summary, "клиент не найден"
	}
	c, err := store.GetCustomer(ctx, s.Store.Pool, sub.CustomerID)
	if err != nil {
		return 0, summary, "клиент не найден"
	}
	summary = c.DisplayName() + " · " + sub.Title
	info, ok := customerWebhookEvent(key)
	if !ok {
		return c.ID, summary, "без уведомления"
	}
	set := s.set().Webhooks
	n, configured := set.Customer[key]
	if !configured {
		n = settingsNotice(info)
	}
	if !n.Enabled {
		return c.ID, summary, "уведомление выключено"
	}
	if c.IsBlocked {
		return c.ID, summary, "клиент заблокирован"
	}
	if info.Repeats && set.RepeatQuietHours > 0 {
		var recent bool
		_ = s.Store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM webhook_events WHERE customer_id=$1 AND event=$2 AND result='клиенту'
			AND created_at > now() - make_interval(hours => $3))`, c.ID, webhookEventName(key), set.RepeatQuietHours).Scan(&recent)
		if recent {
			return c.ID, summary, "повтор пропущен"
		}
	}
	tpl := n.Template
	if strings.TrimSpace(tpl) == "" {
		tpl = info.Template
	}
	all := s.subscriptionVars(c, sub, u)
	for k, v := range vars {
		all[k] = v
	}
	msg := Message{Text: renderVars(tpl, all, true), EmailSubject: info.Title}
	if info.Button != "" {
		msg.ButtonText = info.Button
		msg.ButtonURL = s.Cfg.PublicURL + "/" + strings.TrimPrefix(path, "/")
	}
	s.Notify.Customer(ctx, c, msg)
	return c.ID, summary, "клиенту"
}

// webhookEventName maps a catalog key back to the journal's event name.
func webhookEventName(key string) string {
	if strings.HasPrefix(key, "user.expiration.") {
		return "user.expiration"
	}
	return key
}

func settingsNotice(e CustomerWebhookEvent) settings.CustomerNotice {
	return settings.CustomerNotice{Enabled: e.Default}
}

func (s *Service) subscriptionVars(c *store.Customer, sub *store.Subscription, u *rwUser) map[string]string {
	loc := s.set().General.Location()
	exp := sub.ExpiresAt
	if u != nil && !u.ExpireAt.IsZero() {
		exp = u.ExpireAt
	}
	v := map[string]string{"name": c.DisplayName(), "plan": sub.Title, "expire_date": exp.In(loc).Format("02.01.2006 15:04"),
		"time_left": humanLeft(time.Until(exp))}
	limit := sub.TrafficLimitBytes
	var used int64
	if u != nil {
		limit = u.TrafficLimitBytes
		used = u.UserTraffic.UsedTrafficBytes
	}
	v["used"] = humanBytes(used)
	if limit > 0 {
		v["limit"] = humanBytes(limit)
	} else {
		v["limit"] = "без ограничений"
	}
	return v
}

func humanLeft(d time.Duration) string {
	if d <= 0 {
		return "0 ч"
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%d ч", int(math.Ceil(d.Hours())))
	}
	return fmt.Sprintf("%d дн.", int(math.Round(d.Hours()/24)))
}

func humanSeconds(sec float64) string {
	switch {
	case sec <= 0:
		return "—"
	case sec < 3600:
		return fmt.Sprintf("%d мин", int(math.Ceil(sec/60)))
	case sec < 48*3600:
		return fmt.Sprintf("%d ч", int(math.Round(sec/3600)))
	}
	return fmt.Sprintf("%d дн.", int(math.Round(sec/86400)))
}

func humanBytes(b int64) string {
	const gb = 1 << 30
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.1f ТБ", float64(b)/(1<<40))
	case b >= gb:
		return fmt.Sprintf("%.1f ГБ", float64(b)/gb)
	case b >= 1<<20:
		return fmt.Sprintf("%d МБ", b>>20)
	}
	return fmt.Sprintf("%d КБ", b>>10)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func deviceTitle(d rwDevice) string {
	parts := []string{}
	if m := strings.TrimSpace(deref(d.DeviceModel)); m != "" {
		parts = append(parts, m)
	}
	if p := strings.TrimSpace(deref(d.Platform)); p != "" {
		os := p
		if v := strings.TrimSpace(deref(d.OSVersion)); v != "" {
			os += " " + v
		}
		parts = append(parts, os)
	}
	if len(parts) == 0 {
		return "устройство " + maskHWID(d.HWID)
	}
	return strings.Join(parts, ", ")
}

func maskHWID(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:4] + "…" + h[len(h)-4:]
}

// ---- admin page ----

type WebhookLogItem struct {
	ID         int64     `json:"id"`
	Event      string    `json:"event"`
	CustomerID *int64    `json:"customer_id"`
	Summary    string    `json:"summary"`
	Result     string    `json:"result"`
	CreatedAt  time.Time `json:"created_at"`
}

// WebhookLog returns the latest journal entries and the totals per event for 7 days.
func (s *Service) WebhookLog(ctx context.Context, limit int) (map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT id, event, customer_id, summary, result, created_at FROM webhook_events
		ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WebhookLogItem{}
	for rows.Next() {
		var it WebhookLogItem
		if err := rows.Scan(&it.ID, &it.Event, &it.CustomerID, &it.Summary, &it.Result, &it.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	var last *time.Time
	_ = s.Store.Pool.QueryRow(ctx, `SELECT max(created_at) FROM webhook_events`).Scan(&last)
	return map[string]any{"items": items, "last": last, "url": s.Cfg.PublicURL + "/api/webhooks/remnawave",
		"events": CustomerWebhookEvents}, rows.Err()
}

// TestCustomerWebhook sends a sample customer message to the admin themself.
func (s *Service) TestCustomerWebhook(ctx context.Context, key string, admin *store.Customer) error {
	info, ok := customerWebhookEvent(key)
	if !ok {
		return userErr("неизвестное событие")
	}
	tpl := s.set().Webhooks.Customer[key].Template
	if strings.TrimSpace(tpl) == "" {
		tpl = info.Template
	}
	vars := map[string]string{"name": admin.DisplayName(), "plan": "Бобрёнок", "expire_date": time.Now().Add(72 * time.Hour).In(s.set().General.Location()).Format("02.01.2006 15:04"),
		"time_left": "3 дн.", "used": "42.0 ГБ", "limit": "50.0 ГБ", "percent": "80", "hours": "24", "device": "iPhone 15, iOS 18.1",
		"platform": "iOS", "ip": "203.0.113.7", "server": "Германия", "duration": "1 ч", "action": "блокировка"}
	msg := Message{Text: "🧪 Пример уведомления\n\n" + renderVars(tpl, vars, true), EmailSubject: info.Title}
	if info.Button != "" {
		msg.ButtonText, msg.ButtonURL = info.Button, s.Cfg.PublicURL+"/"
	}
	return s.Notify.Deliver(ctx, admin, msg, "auto")
}

// CleanWebhookLog keeps 30 days of the journal.
func (s *Service) CleanWebhookLog(ctx context.Context) {
	_, _ = s.Store.Pool.Exec(ctx, `DELETE FROM webhook_events WHERE created_at < now() - interval '30 days'`)
}

// SyncFromPanel refreshes every subscription's panel fields (short UUID, username, link, expiry, traffic
// limit) from Remnawave. Used after importing from another shop, where only the panel user id is known.
func (s *Service) SyncFromPanel(ctx context.Context, progress func(done, total int)) (updated int, problems []string, err error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT id FROM subscriptions WHERE rw_user_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return 0, nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for i, id := range ids {
		if progress != nil && i%50 == 0 {
			progress(i, len(ids))
		}
		sub, err := store.GetSubscription(ctx, s.Store.Pool, id)
		if err != nil {
			return updated, problems, err
		}
		u, err := s.Panel.GetUser(ctx, *sub.RWUserID)
		if err != nil {
			problems = append(problems, fmt.Sprintf("подписка #%d (пользователь панели %d): %v", sub.ID, *sub.RWUserID, err))
			continue
		}
		fillFromPanel(sub, u)
		if err := store.UpdateSubscription(ctx, s.Store.Pool, sub); err != nil {
			return updated, problems, err
		}
		updated++
	}
	if progress != nil {
		progress(len(ids), len(ids))
	}
	return updated, problems, nil
}
