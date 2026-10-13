package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"bshop/internal/geoip"
	"bshop/internal/remnawave"
	"bshop/internal/store"
)

// histPanel serves a fixed request history, in the order the test gives it (newest first or oldest first).
type histPanel struct {
	*fakePanel
	recs []remnawave.SubRequest
}

func (h *histPanel) SubscriptionRequests(_ context.Context, start, size int) ([]remnawave.SubRequest, int, error) {
	if start >= len(h.recs) {
		return nil, len(h.recs), nil
	}
	end := start + size
	if end > len(h.recs) {
		end = len(h.recs)
	}
	return h.recs[start:end], len(h.recs), nil
}

func strPtr(s string) *string { return &s }

func TestGeoSync(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	db, err := geoip.Open("../geoip/testdata/mini.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e.svc.GeoOverride = db

	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, RWTag: "BOBR", DeviceLimit: 3,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	cust := login(t, e, 2001, "")
	p := buyPaid(t, e, cust, plan, 0, 0)
	sub, _ := store.GetSubscription(ctx, e.pool, *p.SubscriptionID)
	rw := *sub.RWUserID

	now := time.Now()
	rec := func(id int64, user int64, ip string, ago time.Duration) remnawave.SubRequest {
		return remnawave.SubRequest{ID: id, UserID: user, RequestIP: strPtr(ip), UserAgent: strPtr("Happ/1.0"), RequestedAt: now.Add(-ago)}
	}
	hp := &histPanel{fakePanel: e.panel}
	e.svc.Panel = hp
	// Newest first, as the panel returns it.
	hp.recs = []remnawave.SubRequest{
		rec(9, rw, "5.8.1.1", time.Minute),
		rec(8, rw, "not-an-ip", 2*time.Minute), // ignored
		rec(7, 999, "5.8.1.1", 3*time.Minute),  // a user the shop doesn't know
		rec(6, rw, "10.0.0.7", 4*time.Minute),  // private address
		rec(5, rw, "8.8.8.8", 5*time.Minute),   // not in the database: counted, country unknown
		rec(4, rw, "85.1.2.3", 6*time.Minute),  // Zurich
		rec(3, rw, "85.1.2.3", 7*time.Minute),  // Zurich
		rec(2, rw, "5.8.1.2", 8*time.Minute),   // Barnaul
		rec(1, rw, "5.8.1.1", 9*time.Minute),   // Barnaul
	}
	e.svc.SyncGeo(ctx)
	var n int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM sub_requests`).Scan(&n)
	if n != 6 {
		t.Fatalf("stored %d requests, want 6", n)
	}

	rep, err := e.svc.StatsGeo(ctx, "1m")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 6 || rep.Customers != 1 || len(rep.Countries) != 3 {
		t.Fatalf("report: %+v", rep)
	}
	ru := rep.Countries[0]
	if ru.Code != "RU" || ru.Count != 3 || len(ru.Cities) != 1 || ru.Cities[0].City != "Barnaul" || ru.Cities[0].Region != "Altai Krai" ||
		ru.Cities[0].Lat == nil || *ru.Cities[0].Lat < 53 || *ru.Cities[0].Lat > 54 {
		t.Fatalf("RU: %+v", ru)
	}
	if rep.Countries[1].Code != "CH" || rep.Countries[1].Count != 2 || rep.Countries[2].Code != "" || rep.Countries[2].Count != 1 {
		t.Fatalf("others: %+v", rep.Countries)
	}
	if !rep.Status.Ready || rep.Status.SyncError != "" || rep.Status.SyncedAt == nil {
		t.Fatalf("status: %+v", rep.Status)
	}

	// A second run adds only what is new.
	hp.recs = append([]remnawave.SubRequest{rec(10, rw, "177.1.2.3", 0)}, hp.recs...)
	e.svc.SyncGeo(ctx)
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM sub_requests`).Scan(&n)
	if n != 7 {
		t.Fatalf("after second sync: %d, want 7", n)
	}
	e.svc.SyncGeo(ctx)
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM sub_requests`).Scan(&n)
	if n != 7 {
		t.Fatalf("third sync changed the count: %d", n)
	}

	// The customer's own view.
	cg, err := e.svc.CustomerGeo(ctx, cust.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cg.Total != 7 || len(cg.Recent) != 7 || cg.Recent[0].Country != "BR" || cg.Recent[0].IP != "177.1.2.3" {
		t.Fatalf("customer geo: %+v", cg)
	}

	// Retention: old rows are removed.
	_, _ = e.pool.Exec(ctx, `UPDATE sub_requests SET requested_at = now() - interval '200 days' WHERE panel_id = 1`)
	e.svc.CleanGeo(ctx)
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM sub_requests`).Scan(&n)
	if n != 6 {
		t.Fatalf("after cleanup: %d, want 6", n)
	}
}

// A panel that lists the oldest records first is walked from its last page.
func TestGeoSyncOldestFirst(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	db, err := geoip.Open("../geoip/testdata/mini.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e.svc.GeoOverride = db
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, RWTag: "BOBR", DeviceLimit: 3,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	cust := login(t, e, 2002, "")
	p := buyPaid(t, e, cust, plan, 0, 0)
	sub, _ := store.GetSubscription(ctx, e.pool, *p.SubscriptionID)

	hp := &histPanel{fakePanel: e.panel}
	e.svc.Panel = hp
	const total = 1100
	now := time.Now()
	for i := 1; i <= total; i++ {
		hp.recs = append(hp.recs, remnawave.SubRequest{ID: int64(i), UserID: *sub.RWUserID, RequestIP: strPtr(fmt.Sprintf("5.8.%d.%d", i%200, i%250+1)),
			RequestedAt: now.Add(-time.Duration(total-i) * time.Minute)})
	}
	e.svc.SyncGeo(ctx)
	var n int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM sub_requests`).Scan(&n)
	if n != total {
		t.Fatalf("stored %d, want %d", n, total)
	}
}
