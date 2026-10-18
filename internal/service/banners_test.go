package service_test

import (
	"context"
	"testing"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"
)

func bannerTitles(t *testing.T, e *env, c *store.Customer) []string {
	t.Helper()
	list, err := e.svc.BannersFor(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, b := range list {
		out = append(out, b.Title)
	}
	return out
}

func TestBanners(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := login(t, e, 701, "") // Telegram customer without email, no subscription

	// System banners: Telegram is linked, email is not.
	if got := bannerTitles(t, e, c); len(got) != 1 || got[0] != "Привяжите email" {
		t.Fatalf("system banners = %v", got)
	}

	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	add := func(b store.Banner) *store.Banner {
		b.IsActive = true
		if err := service.ValidateBanner(&b); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveBanner(ctx, e.pool, &b); err != nil {
			t.Fatal(err)
		}
		return &b
	}
	sale := add(store.Banner{Title: "Скидка", Segment: "no_sub", Dismissible: true, ButtonText: "Купить", ButtonURL: "/buy"})
	add(store.Banner{Title: "Только активным", Segment: "active"})
	add(store.Banner{Title: "Ещё не начался", StartsAt: &future})
	add(store.Banner{Title: "Уже закончился", EndsAt: &past})
	if got := bannerTitles(t, e, c); len(got) != 2 || got[1] != "Скидка" {
		t.Fatalf("banners = %v", got)
	}

	// Dismissed banners stay hidden for this customer only.
	if err := e.svc.DismissBanner(ctx, c, sale.ID); err != nil {
		t.Fatal(err)
	}
	if got := bannerTitles(t, e, c); len(got) != 1 {
		t.Fatalf("after dismiss = %v", got)
	}
	other := login(t, e, 702, "")
	if got := bannerTitles(t, e, other); len(got) != 2 {
		t.Fatalf("other customer = %v", got)
	}

	// Validation and system banners cannot be deleted.
	if err := service.ValidateBanner(&store.Banner{Title: "x", ButtonURL: "javascript:alert(1)"}); err == nil {
		t.Fatal("unsafe link accepted")
	}
	list, _ := store.ListBanners(ctx, e.pool)
	if err := store.DeleteBanner(ctx, e.pool, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := store.ListBanners(ctx, e.pool); len(after) != len(list) {
		t.Fatal("system banner deleted")
	}
}
