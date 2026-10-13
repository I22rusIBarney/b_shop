package service_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
)

const testSecret = "AbCdEf0123456789AbCdEf0123456789"

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(testSecret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

func TestRemnawaveWebhooks(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3, TrafficLimitGB: 50,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 15000}}})
	c := login(t, e, 801, "")
	p := buyPaid(t, e, c, plan, 0, 0)
	sub, err := store.GetSubscription(ctx, e.pool, *p.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	rwID := *sub.RWUserID

	user := fmt.Sprintf(`{"id":%d,"username":"u801","status":"EXPIRED","trafficLimitBytes":53687091200,"expireAt":"2026-10-01T00:00:00.000Z",
		"lastTriggeredThreshold":80,"userTraffic":{"usedTrafficBytes":42949672960}}`, rwID)
	expired := `{"scope":"user","event":"user.expired","timestamp":"2026-10-13T10:00:00.000Z","data":` + user + `,"meta":null}`

	// Off by default.
	if err := e.svc.VerifyRemnawaveSignature([]byte(expired), sign(expired)); !errors.Is(err, service.ErrWebhooksOff) {
		t.Fatalf("expected disabled, got %v", err)
	}
	mustSet(t, e, "webhooks", `{"enabled":true,"secret":"`+testSecret+`"}`)
	if err := e.svc.VerifyRemnawaveSignature([]byte(expired), sign(expired+"x")); !errors.Is(err, service.ErrBadSignature) {
		t.Fatalf("bad signature accepted: %v", err)
	}
	if err := e.svc.VerifyRemnawaveSignature([]byte(expired), sign(expired)); err != nil {
		t.Fatal(err)
	}

	before := len(e.notes.customer)
	if err := e.svc.HandleRemnawaveWebhook(ctx, []byte(expired)); err != nil {
		t.Fatal(err)
	}
	if len(e.notes.customer) != before+1 || !strings.Contains(e.notes.customer[len(e.notes.customer)-1], "закончилась") {
		t.Fatalf("customer not notified: %v", e.notes.customer[before:])
	}
	// The panel retried the same delivery: ignored.
	if err := e.svc.HandleRemnawaveWebhook(ctx, []byte(expired)); err != nil {
		t.Fatal(err)
	}
	if len(e.notes.customer) != before+1 {
		t.Fatal("duplicate delivery notified twice")
	}

	// Threshold: percent from the panel, and the repeat pause.
	th := func(ts string) string {
		return `{"scope":"user","event":"user.bandwidth_usage_threshold_reached","timestamp":"` + ts + `","data":` + user + `,"meta":null}`
	}
	_ = e.svc.HandleRemnawaveWebhook(ctx, []byte(th("2026-10-13T11:00:00.000Z")))
	last := e.notes.customer[len(e.notes.customer)-1]
	if !strings.Contains(last, "80%") || !strings.Contains(last, "40.0 ГБ из 50.0 ГБ") {
		t.Fatalf("threshold text: %s", last)
	}
	n := len(e.notes.customer)
	_ = e.svc.HandleRemnawaveWebhook(ctx, []byte(th("2026-10-13T12:00:00.000Z")))
	if len(e.notes.customer) != n {
		t.Fatal("repeat pause ignored")
	}

	// Disabled by default: expiration warning (automations cover it).
	exp := `{"scope":"user","event":"user.expiration","timestamp":"2026-10-13T13:00:00.000Z","data":` + user + `,"meta":{"expiration":-72}}`
	_ = e.svc.HandleRemnawaveWebhook(ctx, []byte(exp))
	if len(e.notes.customer) != n {
		t.Fatal("expiration notice should be off by default")
	}

	// New device goes to the customer and their timeline.
	dev := `{"scope":"user_hwid_devices","event":"user_hwid_devices.added","timestamp":"2026-10-13T14:00:00.000Z","data":{"user":` + user +
		`,"hwidUserDevice":{"hwid":"abcdef123456","userId":1,"platform":"iOS","osVersion":"18.1","deviceModel":"iPhone 15","userAgent":null}}}`
	_ = e.svc.HandleRemnawaveWebhook(ctx, []byte(dev))
	if last := e.notes.customer[len(e.notes.customer)-1]; !strings.Contains(last, "iPhone 15, iOS 18.1") {
		t.Fatalf("device text: %s", last)
	}
	var evs int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM customer_events WHERE customer_id=$1 AND kind='device_added'`, c.ID).Scan(&evs)
	if evs != 1 {
		t.Fatal("device event not in timeline")
	}

	// Node down → admin notification.
	node := `{"scope":"node","event":"node.connection_lost","timestamp":"2026-10-13T15:00:00.000Z","data":{"uuid":"00000000-0000-0000-0000-000000000001","name":"Германия","address":"de.example","countryCode":"DE","isConnected":false}}`
	_ = e.svc.HandleRemnawaveWebhook(ctx, []byte(node))
	got := e.notes.adminNotes[len(e.notes.adminNotes)-1]
	if got.Event != "rw_node_down" || !strings.Contains(got.Text, "Германия") {
		t.Fatalf("node note: %+v", got)
	}

	// Unknown user: journaled, nobody notified.
	other := strings.Replace(expired, fmt.Sprintf(`"id":%d`, rwID), `"id":999999`, 1)
	n = len(e.notes.customer)
	_ = e.svc.HandleRemnawaveWebhook(ctx, []byte(other))
	if len(e.notes.customer) != n {
		t.Fatal("unknown user notified")
	}
	log, err := e.svc.WebhookLog(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	items := log["items"].([]service.WebhookLogItem)
	if len(items) != 7 || items[0].Result != "клиент не найден" {
		t.Fatalf("journal: %+v", items)
	}
}
