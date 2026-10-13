package service_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"bshop/internal/config"
	"bshop/internal/db"
	"bshop/internal/mailer"
	"bshop/internal/remnawave"
	"bshop/internal/secrets"
	"bshop/internal/service"
	"bshop/internal/settings"
	"bshop/internal/store"
	"bshop/internal/yookassa"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const botToken = "123456:TEST-TOKEN"

// ---- fakes ----

type fakePanel struct {
	mu       sync.Mutex
	users    map[int64]*remnawave.User
	nextID   int64
	failNext error
	creates  int
}

func newPanel() *fakePanel { return &fakePanel{users: map[int64]*remnawave.User{}, nextID: 100} }

func (f *fakePanel) take() error {
	err := f.failNext
	f.failNext = nil
	return err
}

func (f *fakePanel) CreateUser(_ context.Context, in remnawave.CreateUser) (*remnawave.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.take(); err != nil {
		return nil, err
	}
	for _, u := range f.users {
		if u.Username == in.Username {
			return nil, &remnawave.APIError{Status: 400, Message: "username taken"}
		}
	}
	f.nextID++
	f.creates++
	u := &remnawave.User{ID: f.nextID, Username: in.Username, ShortUUID: fmt.Sprintf("s%d", f.nextID), Status: "ACTIVE",
		ExpireAt: in.ExpireAt, TrafficLimitBytes: in.TrafficLimitBytes, TrafficLimitStrategy: in.TrafficLimitStrategy,
		Tag: in.Tag, HwidDeviceLimit: in.HwidDeviceLimit, ExternalSquadUUID: in.ExternalSquadUUID,
		SubscriptionURL: fmt.Sprintf("https://sub.example/s%d", f.nextID)}
	for _, id := range in.ActiveInternalSquads {
		u.ActiveInternalSquads = append(u.ActiveInternalSquads, remnawave.Squad{UUID: id})
	}
	f.users[u.ID] = u
	cp := *u
	return &cp, nil
}

func (f *fakePanel) UpdateUser(_ context.Context, in remnawave.UpdateUser) (*remnawave.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.take(); err != nil {
		return nil, err
	}
	u, ok := f.users[in.ID]
	if !ok {
		return nil, remnawave.ErrNotFound
	}
	if in.ExpireAt != nil {
		u.ExpireAt = *in.ExpireAt
	}
	if in.TrafficLimitBytes != nil {
		u.TrafficLimitBytes = *in.TrafficLimitBytes
	}
	if in.HwidDeviceLimit != nil {
		u.HwidDeviceLimit = in.HwidDeviceLimit
	}
	if in.Tag != nil {
		u.Tag = in.Tag
	}
	if in.ClearTag {
		u.Tag = nil
	}
	cp := *u
	return &cp, nil
}

func (f *fakePanel) GetUser(_ context.Context, id int64) (*remnawave.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, remnawave.ErrNotFound
}

func (f *fakePanel) GetUserByUsername(_ context.Context, name string) (*remnawave.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Username == name {
			cp := *u
			return &cp, nil
		}
	}
	return nil, remnawave.ErrNotFound
}

func (f *fakePanel) DeleteUser(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.users, id)
	return nil
}
func (f *fakePanel) ResetTraffic(ctx context.Context, id int64) (*remnawave.User, error) {
	return f.GetUser(ctx, id)
}
func (f *fakePanel) RevokeSubscription(ctx context.Context, id int64, _ bool) (*remnawave.User, error) {
	f.mu.Lock()
	if u, ok := f.users[id]; ok {
		u.ShortUUID += "r"
		u.SubscriptionURL += "r"
	}
	f.mu.Unlock()
	return f.GetUser(ctx, id)
}
func (f *fakePanel) EnableUser(ctx context.Context, id int64) (*remnawave.User, error) {
	return f.GetUser(ctx, id)
}
func (f *fakePanel) DisableUser(ctx context.Context, id int64) (*remnawave.User, error) {
	return f.GetUser(ctx, id)
}
func (f *fakePanel) UserDevices(context.Context, int64) ([]remnawave.Device, error) { return nil, nil }
func (f *fakePanel) DeleteDevice(context.Context, int64, string) error              { return nil }
func (f *fakePanel) DeleteAllDevices(context.Context, int64) error                  { return nil }
func (f *fakePanel) InternalSquads(context.Context) ([]remnawave.Squad, error)      { return nil, nil }
func (f *fakePanel) ExternalSquads(context.Context) ([]remnawave.Squad, error)      { return nil, nil }

type fakeGateway struct {
	mu       sync.Mutex
	payments map[string]*yookassa.Payment
	refunds  map[string]*yookassa.Refund
	created  []yookassa.CreatePayment
	n        int
}

func newGateway() *fakeGateway {
	return &fakeGateway{payments: map[string]*yookassa.Payment{}, refunds: map[string]*yookassa.Refund{}}
}

func (g *fakeGateway) CreatePayment(_ context.Context, _ string, in yookassa.CreatePayment) (*yookassa.Payment, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	p := &yookassa.Payment{ID: fmt.Sprintf("yk-%d", g.n), Status: "pending", Amount: in.Amount, Metadata: in.Metadata,
		Confirmation: &yookassa.Confirmation{Type: "redirect", ConfirmationURL: fmt.Sprintf("https://yoomoney.example/%d", g.n)}}
	g.payments[p.ID] = p
	g.created = append(g.created, in)
	return p, nil
}

func (g *fakeGateway) GetPayment(_ context.Context, id string) (*yookassa.Payment, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.payments[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *p
	return &cp, nil
}

func (g *fakeGateway) GetRefund(_ context.Context, id string) (*yookassa.Refund, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.refunds[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}

func (g *fakeGateway) succeed(id, method string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.payments[id].Status = "succeeded"
	g.payments[id].Paid = true
	g.payments[id].PaymentMethod = &yookassa.PaymentMethod{Type: method}
}

type receipt struct {
	name   string
	amount int64
	at     time.Time
	uuid   string
}

type fakeReceipts struct {
	mu       sync.Mutex
	issued   []receipt
	canceled []string
	fail     error
}

func (r *fakeReceipts) CreateIncome(_ context.Context, name string, k int64, at time.Time) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return "", r.fail
	}
	id := fmt.Sprintf("rcpt-%d", len(r.issued)+1)
	r.issued = append(r.issued, receipt{name, k, at, id})
	return id, nil
}

func (r *fakeReceipts) CancelIncome(_ context.Context, id, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.canceled = append(r.canceled, id)
	return nil
}

type notes struct {
	mu       sync.Mutex
	customer []string
	admin    []string
}

func (n *notes) Customer(_ context.Context, _ *store.Customer, m service.Message) {
	n.mu.Lock()
	n.customer = append(n.customer, m.Text)
	n.mu.Unlock()
}

func (n *notes) Admin(_ context.Context, event, text string, _ int64) {
	n.mu.Lock()
	n.admin = append(n.admin, event+": "+text)
	n.mu.Unlock()
}

// ---- harness ----

type env struct {
	svc      *service.Service
	pool     *pgxpool.Pool
	panel    *fakePanel
	gw       *fakeGateway
	receipts *fakeReceipts
	notes    *notes
}

func setup(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.New(strings.Repeat("k", 32))
	st := settings.NewStore(pool, box)
	if err := st.Load(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{pool: pool, panel: newPanel(), gw: newGateway(), receipts: &fakeReceipts{}, notes: &notes{}}
	e.svc = &service.Service{
		Cfg:              &config.Config{PublicURL: "https://shop.example", BotToken: botToken, AdminTelegram: []int64{1}},
		Store:            store.New(pool),
		Settings:         st,
		Box:              box,
		Panel:            e.panel,
		Mailer:           mailer.New(func() settings.SMTP { return st.Get().SMTP }),
		Notify:           e.notes,
		Log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		GatewayOverride:  e.gw,
		ReceiptsOverride: e.receipts,
	}
	t.Cleanup(pool.Close)
	mustSet(t, e, "yookassa", `{"enabled":true,"shop_id":"1","secret_key":"live_secret","methods":["sbp","bank_card"],"save_card":true,"description_template":"Подписка {plan} на {period}"}`)
	mustSet(t, e, "moynalog", `{"enabled":true,"login":"123","password":"pw","template":"VPN {plan} на {months} мес."}`)
	return e
}

func mustSet(t *testing.T, e *env, key, js string) {
	t.Helper()
	if err := e.svc.Settings.Update(context.Background(), key, json.RawMessage(js)); err != nil {
		t.Fatalf("settings %s: %v", key, err)
	}
}

func initData(t *testing.T, userJSON, startParam string) string {
	vals := url.Values{}
	vals.Set("user", userJSON)
	vals.Set("auth_date", fmt.Sprint(time.Now().Unix()))
	if startParam != "" {
		vals.Set("start_param", startParam)
	}
	keys := []string{}
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+vals.Get(k))
	}
	m := hmac.New(sha256.New, []byte("WebAppData"))
	m.Write([]byte(botToken))
	secret := m.Sum(nil)
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(strings.Join(parts, "\n")))
	vals.Set("hash", hex.EncodeToString(h.Sum(nil)))
	return vals.Encode()
}

func createPlan(t *testing.T, e *env, p store.Plan) *store.Plan {
	t.Helper()
	if err := service.ValidatePlan(&p); err != nil {
		t.Fatal(err)
	}
	err := e.svc.Store.Tx(context.Background(), func(tx pgx.Tx) error { return store.SavePlan(context.Background(), tx, &p) })
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.GetPlan(context.Background(), e.pool, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func login(t *testing.T, e *env, tgID int64, start string) *store.Customer {
	t.Helper()
	sess, err := e.svc.LoginTelegramWebApp(context.Background(),
		initData(t, fmt.Sprintf(`{"id":%d,"first_name":"Тест","username":"user%d"}`, tgID, tgID), start), service.LoginMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return sess.Customer
}

func notify(t *testing.T, e *env, ykID string) {
	t.Helper()
	var n yookassa.Notification
	n.Event = "payment.succeeded"
	n.Object.ID = ykID
	if err := e.svc.HandleYooKassaNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
}

// ---- tests ----

func TestLoginAndLinking(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	c1 := login(t, e, 555, "")
	c2 := login(t, e, 555, "")
	if c1.ID != c2.ID {
		t.Fatalf("same telegram user got two customers: %d %d", c1.ID, c2.ID)
	}
	if c1.RefCode == nil || len(*c1.RefCode) != 8 {
		t.Fatalf("ref code not generated: %v", c1.RefCode)
	}
	admin := login(t, e, 1, "")
	if !admin.IsAdmin {
		t.Fatal("admin from ADMIN_TELEGRAM_IDS must be admin")
	}

	// Referral via start_param.
	invited := login(t, e, 777, "ref_"+*c1.RefCode)
	if invited.ReferrerID == nil || *invited.ReferrerID != c1.ID {
		t.Fatalf("referrer not set: %v", invited.ReferrerID)
	}

	// Bad signature is rejected.
	bad := strings.Replace(initData(t, `{"id":9,"first_name":"x"}`, ""), "hash=", "hash=00", 1)
	if _, err := e.svc.LoginTelegramWebApp(ctx, bad, service.LoginMeta{}); err == nil {
		t.Fatal("tampered initData accepted")
	}

	// Linking a Telegram account that already belongs to someone else fails.
	link := c1.ID
	_, err := e.svc.LoginTelegramWebApp(ctx, initData(t, `{"id":777,"first_name":"x"}`, ""), service.LoginMeta{LinkTo: &link})
	var ue *service.UserError
	if !errors.As(err, &ue) {
		t.Fatalf("expected identity taken error, got %v", err)
	}
}

func TestSettingsSecrets(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var raw string
	if err := e.pool.QueryRow(ctx, `SELECT value->>'secret_key' FROM settings WHERE key='yookassa'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("secret stored in clear: %q", raw)
	}
	pub, _ := e.svc.Settings.Public("yookassa")
	if strings.Contains(string(pub), "live_secret") || !strings.Contains(string(pub), settings.Mask) {
		t.Fatalf("secret leaked or not masked: %s", pub)
	}
	// Sending the mask back keeps the stored value.
	mustSet(t, e, "yookassa", strings.Replace(string(pub), `"shop_id":"1"`, `"shop_id":"2"`, 1))
	if got := e.svc.Settings.Get().YooKassa; got.SecretKey != "live_secret" || got.ShopID != "2" {
		t.Fatalf("mask round trip broke secret: %+v", got)
	}
	// Reload from DB decrypts.
	st := settings.NewStore(e.pool, e.svc.Box)
	if err := st.Load(ctx); err != nil || st.Get().YooKassa.SecretKey != "live_secret" {
		t.Fatalf("reload: %v %q", err, st.Get().YooKassa.SecretKey)
	}
	// Validation errors are reported.
	if err := e.svc.Settings.Update(ctx, "auth", json.RawMessage(`{"telegram_enabled":false,"email_enabled":false}`)); err == nil {
		t.Fatal("disabling every login method must fail")
	}
	// Referral tiers are dynamic and sorted.
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"tiers_enabled":true,"tiers":[{"min_invited":15,"days":7},{"min_invited":5,"days":4}]}`)
	r := e.svc.Settings.Get().Referral
	if r.InviterBonus(0) != 3 || r.InviterBonus(5) != 4 || r.InviterBonus(20) != 7 {
		t.Fatalf("tier bonus wrong: %d %d %d", r.InviterBonus(0), r.InviterBonus(5), r.InviterBonus(20))
	}
}

func TestBuyRenewReceiptRefund(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sq := uuid.New()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3, TrafficLimitGB: 0,
		RWTag: "basic", RWInternalSquads: []uuid.UUID{sq},
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}, {Unit: "month", Amount: 3, Price: 84000, IsBest: true}}})
	c := login(t, e, 1001, "")

	// Personal discount shows in catalog.
	disc := 10
	if err := store.PatchCustomer(ctx, e.pool, c.ID, store.CustomerAdminPatch{PersonalDiscount: &disc}); err != nil {
		t.Fatal(err)
	}
	c, _ = store.GetCustomer(ctx, e.pool, c.ID)
	cat, err := e.svc.Catalog(ctx, c)
	if err != nil || len(cat) != 1 {
		t.Fatalf("catalog: %v %d", err, len(cat))
	}
	if got := cat[0].Offers[0].Price; got != 26910 {
		t.Fatalf("discounted price = %d, want 26910", got)
	}
	if cat[0].Offers[1].FullPrice != 80730 || cat[0].Offers[1].Price != 75600 {
		t.Fatalf("3-month offer = %+v", cat[0].Offers[1])
	}

	// Buy 1 month via SBP.
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfirmationURL == "" || res.Amount != 26910 {
		t.Fatalf("buy result %+v", res)
	}
	if e.gw.created[0].PaymentMethodData.Type != "sbp" || e.gw.created[0].Description != "Подписка Бобрёнок на 1 месяц" {
		t.Fatalf("yookassa request %+v", e.gw.created[0])
	}
	// Webhook says paid; apply twice (duplicate delivery).
	e.gw.succeed("yk-1", "sbp")
	notify(t, e, "yk-1")
	notify(t, e, "yk-1")
	subs, _ := store.ListSubscriptions(ctx, e.pool, c.ID)
	if len(subs) != 1 || e.panel.creates != 1 {
		t.Fatalf("subs=%d panel creates=%d", len(subs), e.panel.creates)
	}
	sub := subs[0]
	if !sub.IsPrimary || sub.Kind != "paid" || sub.RWUserID == nil {
		t.Fatalf("subscription %+v", sub)
	}
	pu := e.panel.users[*sub.RWUserID]
	if pu.Tag == nil || *pu.Tag != "BASIC" || *pu.HwidDeviceLimit != 3 || pu.ActiveInternalSquads[0].UUID != sq {
		t.Fatalf("panel user params wrong: %+v", pu)
	}
	if d := time.Until(sub.ExpiresAt); d < 30*24*time.Hour || d > 31*24*time.Hour+time.Minute {
		t.Fatalf("expiry %v", d)
	}

	// Renew 3 months: expiry extends from current expiry, no new panel user.
	res, err = e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[1].ID, SubscriptionID: sub.ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	e.gw.succeed("yk-2", "sbp")
	notify(t, e, "yk-2")
	renewed, _ := store.GetSubscription(ctx, e.pool, sub.ID)
	if got := renewed.ExpiresAt.Sub(sub.ExpiresAt); got < 93*24*time.Hour-time.Minute || got > 93*24*time.Hour+time.Minute {
		t.Fatalf("renew added %v, want 93 days", got)
	}
	if e.panel.creates != 1 {
		t.Fatalf("renew created a new panel user")
	}

	// Receipts: issued with the payment time and the plan name.
	e.svc.ProcessReceipts(ctx)
	if len(e.receipts.issued) != 2 {
		t.Fatalf("receipts issued = %d", len(e.receipts.issued))
	}
	r0 := e.receipts.issued[0]
	p1, _ := store.GetPaymentByProviderRef(ctx, e.pool, "yookassa", "yk-1")
	if r0.name != "VPN Бобрёнок на 1 мес." || r0.amount != 26910 || !r0.at.Equal(*p1.PaidAt) {
		t.Fatalf("receipt %+v paid_at %v", r0, p1.PaidAt)
	}
	if e.receipts.issued[1].name != "VPN Бобрёнок на 3 мес." {
		t.Fatalf("second receipt name %q", e.receipts.issued[1].name)
	}
	e.svc.ProcessReceipts(ctx)
	if len(e.receipts.issued) != 2 {
		t.Fatal("receipts issued twice")
	}

	// Refund annuls the receipt.
	e.gw.refunds["rf-1"] = &yookassa.Refund{ID: "rf-1", PaymentID: "yk-1", Status: "succeeded", Amount: yookassa.Kopecks(26910)}
	var n yookassa.Notification
	n.Event = "refund.succeeded"
	n.Object.ID = "rf-1"
	if err := e.svc.HandleYooKassaNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	e.svc.ProcessReceipts(ctx)
	if len(e.receipts.canceled) != 1 || e.receipts.canceled[0] != r0.uuid {
		t.Fatalf("receipt not canceled: %v", e.receipts.canceled)
	}
	p1, _ = store.GetPayment(ctx, e.pool, p1.ID)
	if p1.Status != "refunded" || p1.ReceiptStatus != "canceled" {
		t.Fatalf("after refund: %s %s", p1.Status, p1.ReceiptStatus)
	}
}

func TestPanelFailureRetriesOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "AI", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 9900}}})
	c := login(t, e, 2002, "")
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"}); err != nil {
		t.Fatal(err)
	}
	e.gw.succeed("yk-1", "sbp")
	e.panel.failNext = errors.New("panel down")
	var n yookassa.Notification
	n.Event = "payment.succeeded"
	n.Object.ID = "yk-1"
	if err := e.svc.HandleYooKassaNotification(ctx, n); err == nil {
		t.Fatal("expected failure while panel is down")
	}
	p, _ := store.GetPaymentByProviderRef(ctx, e.pool, "yookassa", "yk-1")
	if p.Status != "succeeded" || p.AppliedAt != nil {
		t.Fatalf("payment state %s applied=%v", p.Status, p.AppliedAt)
	}
	// Worker retries later.
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET paid_at = now() - interval '5 minutes'`); err != nil {
		t.Fatal(err)
	}
	e.svc.ReconcilePayments(ctx)
	e.svc.ReconcilePayments(ctx)
	subs, _ := store.ListSubscriptions(ctx, e.pool, c.ID)
	if len(subs) != 1 || e.panel.creates != 1 {
		t.Fatalf("after retry subs=%d creates=%d", len(subs), e.panel.creates)
	}
}

func TestTrialOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Пробный", IsActive: true, IsTrial: true, DeviceLimit: 2, TrafficLimitGB: 20})
	mustSet(t, e, "trial", fmt.Sprintf(`{"enabled":true,"plan_id":%d,"days":3}`, plan.ID))
	c := login(t, e, 3003, "")
	sub, err := e.svc.ActivateTrial(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Kind != "trial" || sub.TrafficLimitBytes != 20<<30 {
		t.Fatalf("trial sub %+v", sub)
	}
	if _, err := e.svc.ActivateTrial(ctx, c); err == nil {
		t.Fatal("second trial allowed")
	}
	// Trial plans are not sold in the catalog.
	cat, _ := e.svc.Catalog(ctx, c)
	if len(cat) != 0 {
		t.Fatalf("trial plan visible in catalog")
	}
}

func TestPurchaseLimits(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	base := createPlan(t, e, store.Plan{Name: "Base", IsActive: true, MaxPerCustomer: 1,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 10000}}})
	addon := createPlan(t, e, store.Plan{Name: "Addon", IsActive: true, RequiredPlanID: &base.ID,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 5000}}})
	c := login(t, e, 4004, "")
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: addon.ID, PeriodID: addon.Periods[0].ID, Method: "sbp"}); err == nil {
		t.Fatal("addon bought without required plan")
	}
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: base.ID, PeriodID: base.Periods[0].ID, Method: "qiwi"}); err == nil {
		t.Fatal("unknown payment method accepted")
	}
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: base.ID, PeriodID: base.Periods[0].ID, Method: "sbp"}); err != nil {
		t.Fatal(err)
	}
	e.gw.succeed("yk-1", "sbp")
	notify(t, e, "yk-1")
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: base.ID, PeriodID: base.Periods[0].ID, Method: "sbp"}); err == nil {
		t.Fatal("max_per_customer not enforced")
	}
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: addon.ID, PeriodID: addon.Periods[0].ID, Method: "sbp"}); err != nil {
		t.Fatalf("addon with required plan: %v", err)
	}
	// 100% discount completes without the gateway.
	full := 100
	_ = store.PatchCustomer(ctx, e.pool, c.ID, store.CustomerAdminPatch{PersonalDiscount: &full})
	c, _ = store.GetCustomer(ctx, e.pool, c.ID)
	subs, _ := store.ListSubscriptions(ctx, e.pool, c.ID)
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: base.ID, PeriodID: base.Periods[0].ID, SubscriptionID: subs[0].ID})
	if err != nil || !res.Applied {
		t.Fatalf("free renew: %v %+v", err, res)
	}
	e.svc.ProcessReceipts(ctx)
	for _, r := range e.receipts.issued {
		if r.amount == 0 {
			t.Fatal("receipt issued for a free purchase")
		}
	}
}
