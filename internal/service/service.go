// Package service holds the shop's business rules: logins, purchases, provisioning, receipts.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"bshop/internal/config"
	"bshop/internal/geoip"
	"bshop/internal/mailer"
	"bshop/internal/moynalog"
	"bshop/internal/remnawave"
	"bshop/internal/secrets"
	"bshop/internal/settings"
	"bshop/internal/store"
	"bshop/internal/yookassa"

	"github.com/google/uuid"
)

// Panel is the subset of the Remnawave client the service uses (faked in tests).
type Panel interface {
	CreateUser(ctx context.Context, in remnawave.CreateUser) (*remnawave.User, error)
	UpdateUser(ctx context.Context, in remnawave.UpdateUser) (*remnawave.User, error)
	GetUser(ctx context.Context, id int64) (*remnawave.User, error)
	GetUserByUsername(ctx context.Context, username string) (*remnawave.User, error)
	DeleteUser(ctx context.Context, id int64) error
	ResetTraffic(ctx context.Context, id int64) (*remnawave.User, error)
	RevokeSubscription(ctx context.Context, id int64, onlyPasswords bool) (*remnawave.User, error)
	EnableUser(ctx context.Context, id int64) (*remnawave.User, error)
	DisableUser(ctx context.Context, id int64) (*remnawave.User, error)
	UserDevices(ctx context.Context, userID int64) ([]remnawave.Device, error)
	DeleteDevice(ctx context.Context, userID int64, hwid string) error
	DeleteAllDevices(ctx context.Context, userID int64) error
	InternalSquads(ctx context.Context) ([]remnawave.Squad, error)
	ExternalSquads(ctx context.Context) ([]remnawave.Squad, error)
	SubpageConfigs(ctx context.Context) ([]remnawave.SubpageConfigInfo, error)
	ListUsers(ctx context.Context, fn func([]remnawave.User) error) error
	SubpageConfig(ctx context.Context, id uuid.UUID) (json.RawMessage, error)
	Nodes(ctx context.Context) ([]remnawave.Node, error)
}

// PaymentGateway is the subset of the YooKassa client the service uses.
type PaymentGateway interface {
	CreatePayment(ctx context.Context, idemKey string, in yookassa.CreatePayment) (*yookassa.Payment, error)
	GetPayment(ctx context.Context, id string) (*yookassa.Payment, error)
	GetRefund(ctx context.Context, id string) (*yookassa.Refund, error)
}

// Receipts is the subset of the Moy Nalog client the service uses.
type Receipts interface {
	CreateIncome(ctx context.Context, name string, kopecks int64, operationTime time.Time) (string, error)
	CancelIncome(ctx context.Context, receiptUUID, comment string) error
}

// Notifier delivers messages to customers (Telegram / email) and admin event chats.
type Notifier interface {
	Customer(ctx context.Context, c *store.Customer, msg Message)
	Admin(ctx context.Context, n AdminNote)
	// Deliver sends a message through one channel ("telegram", "email" or "auto": Telegram, else email).
	Deliver(ctx context.Context, c *store.Customer, msg Message, channel string) error
}

// Message is a customer notification; buttons are optional.
type Message struct {
	Text         string // Telegram HTML
	ButtonText   string
	ButtonURL    string
	Buttons      []Button // extra buttons, one per row
	Photo        string   // absolute image URL
	EmailSubject string
}

type Button struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type Service struct {
	Cfg      *config.Config
	Store    *store.Store
	Settings *settings.Store
	Box      *secrets.Box
	Panel    Panel
	Mailer   *mailer.Mailer
	Notify   Notifier
	// Channels checks required channel subscriptions; nil without a bot.
	Channels ChannelChecker
	Log      *slog.Logger
	// BotUsername is the Telegram bot's username (without @), for deep links.
	BotUsername string

	// Gateway / Receipts may be overridden in tests; otherwise built from settings on demand.
	GatewayOverride  PaymentGateway
	ReceiptsOverride Receipts

	// Geography of subscription requests (geo.go). GeoOverride replaces the DB-IP database in tests.
	GeoOverride geoip.Lookuper
	geoLoad     sync.Mutex
	geoMu       sync.Mutex
	geo         geoState

	mu         sync.Mutex
	nalog      *moynalog.Client
	nalogKey   string
	applyLocks sync.Map

	// Locations shown to customers, cached briefly so the page does not hit the panel on every open.
	locMu    sync.Mutex
	locCache []Location
	locAt    time.Time

	guideMu       sync.Mutex
	guides        map[string]cachedGuide // by subscription page config uuid ("" = default)
	squadSubpages map[uuid.UUID]string   // external squad → its subscription page config
	squadsAt      time.Time

	importOnce sync.Once
	imp        *importJob
	syncOnce   sync.Once
	psync      *panelSync
}

func (s *Service) set() *settings.All { return s.Settings.Get() }

var ErrPaymentsDisabled = errors.New("оплата временно недоступна")

// ErrUnreachable: the customer has no working channel (bot blocked / no Telegram, no email or SMTP off).
var ErrUnreachable = errors.New("клиенту некуда отправить сообщение")

func (s *Service) gateway() (PaymentGateway, error) {
	if s.GatewayOverride != nil {
		return s.GatewayOverride, nil
	}
	y := s.set().YooKassa
	if !y.Enabled || y.ShopID == "" || y.SecretKey == "" {
		return nil, ErrPaymentsDisabled
	}
	return yookassa.New(y.APIURL, y.ShopID, y.SecretKey), nil
}

func (s *Service) receipts() Receipts {
	if s.ReceiptsOverride != nil {
		return s.ReceiptsOverride
	}
	m := s.set().MoyNalog
	if !m.Enabled {
		return nil
	}
	key := m.APIURL + "|" + m.Login + "|" + m.Password
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nalog == nil || s.nalogKey != key {
		s.nalog = moynalog.New(m.APIURL, m.Login, m.Password)
		s.nalogKey = key
	}
	return s.nalog
}

// UserError is an error whose message is safe and meant to be shown to the user.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func userErr(msg string) error { return &UserError{Msg: msg} }

// lock serializes work on one key (e.g. a payment id) within this process.
func (s *Service) lock(key string) func() {
	v, _ := s.applyLocks.LoadOrStore(key, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// RefLink is the customer's referral link (empty without a bot or code).
func (s *Service) RefLink(c *store.Customer) string {
	if c.RefCode == nil || s.BotUsername == "" {
		return ""
	}
	return "https://t.me/" + s.BotUsername + "?start=ref_" + *c.RefCode
}
