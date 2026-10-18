package settings

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ---- admin notifications ----

// NotifyRoute says where one event type goes. Empty template = built-in text.
type NotifyRoute struct {
	Disabled bool   `json:"disabled"`
	ChatID   int64  `json:"chat_id"`  // 0 = default chat
	TopicID  int    `json:"topic_id"` // forum topic (message_thread_id), 0 = none
	Template string `json:"template"`
}

type Notifications struct {
	// Default destination for events without their own chat. 0 = admins from ADMIN_TELEGRAM_IDS (private chats).
	DefaultChatID  int64                  `json:"default_chat_id"`
	DefaultTopicID int                    `json:"default_topic_id"`
	Events         map[string]NotifyRoute `json:"events"`
}

func defaultNotifications() Notifications {
	return Notifications{Events: map[string]NotifyRoute{}}
}

func (n *Notifications) Validate() error {
	if n.Events == nil {
		n.Events = map[string]NotifyRoute{}
	}
	for k, r := range n.Events {
		if r.TopicID < 0 {
			return fmt.Errorf("%s: неверный номер топика", k)
		}
		if len(r.Template) > 3500 {
			return fmt.Errorf("%s: шаблон длиннее 3500 символов", k)
		}
	}
	return nil
}

// Route returns where an event goes and whether it is enabled.
func (n Notifications) Route(event string) (chatID int64, topicID int, template string, enabled bool) {
	r := n.Events[event]
	if r.Disabled {
		return 0, 0, "", false
	}
	chatID, topicID = r.ChatID, r.TopicID
	if chatID == 0 {
		chatID, topicID = n.DefaultChatID, n.DefaultTopicID
		if r.TopicID != 0 {
			topicID = r.TopicID
		}
	}
	return chatID, topicID, r.Template, true
}

// ---- support ----

type Support struct {
	Enabled bool `json:"enabled"`
	// BotChat: messages written to the bot become ticket messages.
	BotChat                  bool   `json:"bot_chat"`
	MaxOpen                  int    `json:"max_open"`
	AutocloseWaitingMinutes  int    `json:"autoclose_waiting_minutes"`
	AutocloseResolvedMinutes int    `json:"autoclose_resolved_minutes"`
	EmailReplyDelayMinutes   int    `json:"email_reply_delay_minutes"`
	AttachMaxMB              int    `json:"attach_max_mb"`
	AttachMaxFiles           int    `json:"attach_max_files"`
	RatingEnabled            bool   `json:"rating_enabled"`
	RatingDelayMinutes       int    `json:"rating_delay_minutes"`
	NewTicketText            string `json:"new_ticket_text"`
	WorkHours                string `json:"work_hours"` // "10:00-22:00", empty = always
	OffHoursText             string `json:"off_hours_text"`
}

func defaultSupport() Support {
	return Support{
		Enabled:                  true,
		BotChat:                  true,
		MaxOpen:                  3,
		AutocloseWaitingMinutes:  60,
		AutocloseResolvedMinutes: 60,
		EmailReplyDelayMinutes:   10,
		AttachMaxMB:              10,
		AttachMaxFiles:           5,
		RatingEnabled:            true,
		RatingDelayMinutes:       180,
		NewTicketText:            "Обращение создано. Ответим в ближайшее время.",
		OffHoursText:             "Сейчас нерабочее время, оператор ответит утром. Ассистент постарается помочь уже сейчас.",
	}
}

func (s *Support) Validate() error {
	if s.MaxOpen < 0 || s.AutocloseWaitingMinutes < 0 || s.AutocloseResolvedMinutes < 0 || s.EmailReplyDelayMinutes < 0 || s.RatingDelayMinutes < 0 {
		return errors.New("значения не могут быть отрицательными")
	}
	if s.AttachMaxMB < 0 || s.AttachMaxMB > 20 {
		return errors.New("размер вложения: от 0 до 20 МБ")
	}
	if s.AttachMaxFiles < 0 || s.AttachMaxFiles > 10 {
		return errors.New("вложений в сообщении: от 0 до 10")
	}
	if _, _, ok := ParseWindow(s.WorkHours); s.WorkHours != "" && !ok {
		return errors.New("рабочие часы в формате 10:00-22:00")
	}
	return nil
}

// ParseWindow parses "HH:MM-HH:MM" into minutes from midnight.
func ParseWindow(w string) (from, to int, ok bool) {
	a, b, found := strings.Cut(strings.TrimSpace(w), "-")
	if !found {
		return 0, 0, false
	}
	f, ok1 := parseClock(a)
	t, ok2 := parseClock(b)
	return f, t, ok1 && ok2
}

func parseClock(s string) (int, bool) {
	tm, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return tm.Hour()*60 + tm.Minute(), true
}

// InWindow reports whether minute-of-day m is inside [from, to), wrapping over midnight.
func InWindow(m, from, to int) bool {
	if from == to {
		return true
	}
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

// ---- AI assistant ----

type AI struct {
	Enabled bool `json:"enabled"`
	// OpenAI-compatible endpoint (OpenRouter, own Gemini proxy...): {base_url}/chat/completions.
	BaseURL         string   `json:"base_url"`
	APIKey          string   `json:"api_key" secret:"true"`
	Model           string   `json:"model"`
	FallbackModels  []string `json:"fallback_models"`
	VisionModel     string   `json:"vision_model"` // empty = main model
	AnalyzeImages   bool     `json:"analyze_images"`
	ReasoningEffort string   `json:"reasoning_effort"` // "", low, medium, high
	SystemPrompt    string   `json:"system_prompt"`
	UseCustomerData bool     `json:"use_customer_data"`
	UseKnowledge    bool     `json:"use_knowledge"`
	// UseLiveData adds traffic, devices, online time and recent payments from the panel and the shop.
	UseLiveData bool `json:"use_live_data"`
	// UseServerStatus adds which servers are up or down (names only).
	UseServerStatus bool `json:"use_server_status"`
	// KBFromTickets: the assistant proposes knowledge-base articles from closed tickets with operator answers.
	KBFromTickets bool `json:"kb_from_tickets"`
	// Replies below this confidence become drafts for an operator.
	Confidence      float64 `json:"confidence"`
	DebounceSeconds int     `json:"debounce_seconds"`
	MaxCallsPerHour int     `json:"max_calls_per_hour"`
	MaxTokens       int     `json:"max_tokens"`
	Temperature     float64 `json:"temperature"`
	HistoryMessages int     `json:"history_messages"`
	TimeoutSeconds  int     `json:"timeout_seconds"`
	KnowledgeChars  int     `json:"knowledge_chars"`
	NotifyDrafts    bool    `json:"notify_drafts"`
}

func defaultAI() AI {
	return AI{
		BaseURL:         "https://openrouter.ai/api/v1",
		Model:           "google/gemini-2.5-flash",
		FallbackModels:  []string{},
		AnalyzeImages:   true,
		ReasoningEffort: "",
		SystemPrompt: "Ты — вежливый ассистент поддержки VPN-сервиса. Отвечай коротко и по делу, на языке клиента. " +
			"Помогай с установкой приложения, добавлением подписки и типичными проблемами подключения. " +
			"Не выдумывай цены, сроки и возможности — бери их только из данных ниже. " +
			"Если вопрос про деньги, возврат, ошибку оплаты или ты не уверен — передай оператору.",
		UseCustomerData: true,
		UseKnowledge:    true,
		UseLiveData:     true,
		UseServerStatus: true,
		Confidence:      0.75,
		DebounceSeconds: 30,
		MaxCallsPerHour: 3,
		MaxTokens:       4096,
		Temperature:     0.1,
		HistoryMessages: 12,
		TimeoutSeconds:  120,
		KnowledgeChars:  64000,
		NotifyDrafts:    true,
	}
}

func (a *AI) Validate() error {
	a.BaseURL = strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if a.Enabled && (a.BaseURL == "" || a.Model == "") {
		return errors.New("для включения ассистента укажите адрес API и модель")
	}
	if a.Confidence < 0 || a.Confidence > 1 {
		return errors.New("порог уверенности: от 0 до 1")
	}
	if a.Temperature < 0 || a.Temperature > 2 {
		return errors.New("temperature: от 0 до 2")
	}
	switch a.ReasoningEffort {
	case "", "low", "medium", "high":
	default:
		return errors.New("reasoning: пусто, low, medium или high")
	}
	if a.HistoryMessages < 1 || a.HistoryMessages > 100 || a.MaxTokens < 64 || a.TimeoutSeconds < 5 || a.TimeoutSeconds > 600 {
		return errors.New("проверьте лимиты: история 1–100, токенов от 64, таймаут 5–600 с")
	}
	if a.DebounceSeconds < 0 || a.MaxCallsPerHour < 0 || a.KnowledgeChars < 0 {
		return errors.New("значения не могут быть отрицательными")
	}
	return nil
}

// ---- automations ----

type Automations struct {
	Enabled bool `json:"enabled"`
	// Quiet hours in the shop timezone ("22:00-09:00"); messages due then wait until the end.
	QuietHours string `json:"quiet_hours"`
	// Trigger windows.
	ExpiringSoonDays     int `json:"expiring_soon_days"`     // expiring_soon: paid subscription ends within N days
	RecentlyExpiredHours int `json:"recently_expired_hours"` // recently_expired: ended within N hours
	AbandonedCartHours   int `json:"abandoned_cart_hours"`   // abandoned_cart: unpaid invoice from the last N hours
	ActiveTrialHours     int `json:"active_trial_hours"`     // active_trial: trial started within N hours
	InactiveDays         int `json:"inactive_days"`          // inactive_30d: not seen for N days, no active subscription
	// Lead temperature for customers who never paid.
	HotSeenDays        int `json:"hot_seen_days"`        // seen recently and tried to pay
	WarmSeenDays       int `json:"warm_seen_days"`       // seen recently and used the trial
	CoolSeenDays       int `json:"cool_seen_days"`       // seen within N days; older = cold
	LeadMinAgeHours    int `json:"lead_min_age_hours"`   // ignore brand-new customers
	StableMinPayments  int `json:"stable_min_payments"`  // stable: paying customers with N+ payments
	StableMinDays      int `json:"stable_min_days"`      // ... and first payment at least N days ago
	PersonalPromoHours int `json:"personal_promo_hours"` // default validity of {promo:...} codes, hours
}

func defaultAutomations() Automations {
	return Automations{
		Enabled:              false,
		QuietHours:           "22:00-09:00",
		ExpiringSoonDays:     3,
		RecentlyExpiredHours: 72,
		AbandonedCartHours:   48,
		ActiveTrialHours:     24,
		InactiveDays:         30,
		HotSeenDays:          3,
		WarmSeenDays:         7,
		CoolSeenDays:         30,
		LeadMinAgeHours:      24,
		StableMinPayments:    2,
		StableMinDays:        30,
		PersonalPromoHours:   48,
	}
}

func (a *Automations) Validate() error {
	if a.QuietHours != "" {
		if _, _, ok := ParseWindow(a.QuietHours); !ok {
			return errors.New("тихие часы в формате 22:00-09:00")
		}
	}
	for _, v := range []int{a.ExpiringSoonDays, a.RecentlyExpiredHours, a.AbandonedCartHours, a.ActiveTrialHours, a.InactiveDays,
		a.HotSeenDays, a.WarmSeenDays, a.CoolSeenDays, a.LeadMinAgeHours, a.StableMinPayments, a.StableMinDays, a.PersonalPromoHours} {
		if v < 0 {
			return errors.New("значения не могут быть отрицательными")
		}
	}
	return nil
}

// ---- broadcasts ----

type Broadcasts struct {
	RatePerSecond int `json:"rate_per_second"`
}

func (b *Broadcasts) Validate() error {
	if b.RatePerSecond < 1 || b.RatePerSecond > 30 {
		return errors.New("сообщений в секунду: от 1 до 30 (лимит Telegram)")
	}
	return nil
}
