package service_test

import (
	"context"
	"testing"
)

type fakePhotos struct {
	calls int
	photo []byte
}

func (f *fakePhotos) UserPhoto(context.Context, int64) ([]byte, error) {
	f.calls++
	return f.photo, nil
}

func TestAvatarCache(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := login(t, e, 777, "")
	ph := &fakePhotos{photo: []byte("\xff\xd8\xffJPEG")}
	e.svc.Photos = ph

	for i := 0; i < 3; i++ {
		got, err := e.svc.Avatar(ctx, c.ID)
		if err != nil || string(got) != string(ph.photo) {
			t.Fatalf("avatar %d: %q %v", i, got, err)
		}
	}
	if ph.calls != 1 {
		t.Fatalf("Telegram asked %d times, want 1 (cached)", ph.calls)
	}

	// No photo is cached too, so a long customer list does not hit Telegram on every view.
	none := login(t, e, 778, "")
	ph.photo = nil
	for i := 0; i < 2; i++ {
		if got, _ := e.svc.Avatar(ctx, none.ID); len(got) != 0 {
			t.Fatalf("expected no photo, got %q", got)
		}
	}
	if ph.calls != 2 {
		t.Fatalf("calls = %d, want 2", ph.calls)
	}
}
