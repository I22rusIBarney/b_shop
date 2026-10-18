package backup

import (
	"bytes"
	"testing"
	"time"
)

func TestEncryptRoundTrip(t *testing.T) {
	arc, err := Archive([]byte("DUMP"), EnvFile([]string{"SECRET_KEY=abc", "PATH=/bin", "DATABASE_URL=x", "BOT_TOKEN=1:2"}), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	file, err := Encrypt("correct horse", arc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file, []byte("SECRET_KEY")) {
		t.Fatal("plain text in the encrypted file")
	}
	if _, err := Decrypt("wrong password", file); err != ErrPassword {
		t.Fatalf("wrong password: %v", err)
	}
	tampered := bytes.Clone(file)
	tampered[10] ^= 1 // salt
	if _, err := Decrypt("correct horse", tampered); err != ErrPassword {
		t.Fatalf("tampered header: %v", err)
	}
	plain, err := Decrypt("correct horse", file)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Unpack(plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["db.dump"]) != "DUMP" {
		t.Fatalf("dump: %q", files["db.dump"])
	}
	env := string(files["env"])
	if !bytes.Contains(files["env"], []byte("SECRET_KEY=abc")) || !bytes.Contains(files["env"], []byte("BOT_TOKEN=1:2")) {
		t.Fatalf("env misses shop settings: %s", env)
	}
	if bytes.Contains(files["env"], []byte("PATH=")) || bytes.Contains(files["env"], []byte("DATABASE_URL")) {
		t.Fatalf("env has container variables: %s", env)
	}
}
