package yookassa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListPaymentsQuery(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`{"type":"list","items":[{"id":"p1","status":"succeeded","amount":{"value":"299.00","currency":"RUB"},
			"refunded_amount":{"value":"100.00","currency":"RUB"},"metadata":{"payment_id":"x"}}],"next_cursor":"c2"}`))
	}))
	defer srv.Close()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	l, err := New(srv.URL, "shop", "key").ListPayments(context.Background(), "succeeded", from, from.AddDate(0, 0, 2), "c1")
	if err != nil {
		t.Fatal(err)
	}
	q := got.URL.Query()
	if got.URL.Path != "/payments" || q.Get("status") != "succeeded" || q.Get("cursor") != "c1" || q.Get("limit") != "100" ||
		q.Get("created_at.gte") != "2026-10-01T00:00:00Z" || q.Get("created_at.lt") != "2026-10-03T00:00:00Z" {
		t.Fatalf("request %s", got.URL)
	}
	if u, p, _ := got.BasicAuth(); u != "shop" || p != "key" {
		t.Fatal("no basic auth")
	}
	if len(l.Items) != 1 || l.NextCursor != "c2" || l.Items[0].RefundedAmount.ToKopecks() != 10000 || l.Items[0].Amount.ToKopecks() != 29900 {
		t.Fatalf("list %+v", l)
	}
}
