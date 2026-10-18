package service_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"bshop/internal/backup"
)

type fakeFiles struct {
	chats []int64
	name  string
	data  []byte
}

func (f *fakeFiles) SendFile(_ context.Context, chat int64, name string, data []byte, _ string) error {
	f.chats = append(f.chats, chat)
	f.name, f.data = name, data
	return nil
}

func TestBackupToAdmins(t *testing.T) {
	e := setup(t)
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump not installed")
	}
	ctx := context.Background()
	files := &fakeFiles{}
	e.svc.Files = files
	e.svc.Cfg.DatabaseURL = testDSN()

	if _, err := e.svc.MakeBackup(ctx, true); err == nil || !strings.Contains(err.Error(), "пароль") {
		t.Fatalf("no password: %v", err)
	}
	if err := e.svc.Settings.Update(ctx, "backups", []byte(`{"enabled":true,"hour":4,"password":"short"}`)); err == nil {
		t.Fatal("short password accepted")
	}
	mustSet(t, e, "backups", `{"enabled":true,"hour":4,"password":"very-long-password"}`)

	msg, err := e.svc.MakeBackup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files.chats) != 1 || files.chats[0] != 1 || !strings.HasSuffix(files.name, backup.Ext) {
		t.Fatalf("sent %v %q (%s)", files.chats, files.name, msg)
	}
	plain, err := backup.Decrypt("very-long-password", files.data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := backup.Unpack(plain)
	if err != nil {
		t.Fatal(err)
	}
	// A real custom-format dump that pg_restore can list.
	cmd := exec.Command("pg_restore", "--list")
	cmd.Stdin = strings.NewReader(string(got["db.dump"]))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "backup_runs") {
		t.Fatalf("pg_restore --list: %v\n%s", err, out)
	}

	// The settings page shows the masked password; recipients are not configurable there.
	pub, _ := e.svc.Settings.Public("backups")
	if strings.Contains(string(pub), "very-long-password") {
		t.Fatal("password leaked to the admin UI")
	}
}

func testDSN() string { return os.Getenv("TEST_DATABASE_URL") }
