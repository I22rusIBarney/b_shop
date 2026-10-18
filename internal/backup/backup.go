// Package backup makes an encrypted archive of the shop: a pg_dump of the database plus the settings from .env
// (SECRET_KEY decrypts the secrets stored in the database, so a dump alone would not be enough to start again).
//
// File format: "BSHOPBK1" | salt (16) | nonce (12) | AES-256-GCM(tar.gz). The key is scrypt(password, salt).
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

const magic = "BSHOPBK1"

// Ext is the file extension of encrypted backups.
const Ext = ".bshop"

var ErrPassword = errors.New("неверный пароль или файл повреждён")

func key(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 1<<15, 8, 1, 32)
}

// Encrypt seals data with a key derived from password.
func Encrypt(password string, data []byte) ([]byte, error) {
	if password == "" {
		return nil, errors.New("пустой пароль")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	k, err := key(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+len(salt)+len(nonce)+len(data)+gcm.Overhead())
	out = append(out, magic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	// The header is authenticated too, so a changed salt or nonce fails like a wrong password.
	return gcm.Seal(out, nonce, data, out[:len(magic)+len(salt)+len(nonce)]), nil
}

// Decrypt opens a file made by Encrypt.
func Decrypt(password string, file []byte) ([]byte, error) {
	if len(file) < len(magic)+16+12+16 || string(file[:len(magic)]) != magic {
		return nil, errors.New("это не файл резервной копии BobrGO")
	}
	salt := file[len(magic) : len(magic)+16]
	k, err := key(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	head := len(magic) + 16 + gcm.NonceSize()
	plain, err := gcm.Open(nil, file[len(magic)+16:head], file[head:], file[:head])
	if err != nil {
		return nil, ErrPassword
	}
	return plain, nil
}

// skipEnv are variables of the container itself, not of the shop's .env.
var skipEnv = map[string]bool{"PATH": true, "HOME": true, "HOSTNAME": true, "PWD": true, "SHLVL": true, "TERM": true, "DATABASE_URL": true}

// EnvFile renders the process environment as a .env file (the container gets the whole .env through env_file).
func EnvFile(environ []string) []byte {
	var lines []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || skipEnv[name] || strings.HasPrefix(name, "GO") {
			continue
		}
		lines = append(lines, kv)
	}
	sort.Strings(lines)
	return []byte("# Настройки из резервной копии BobrGO Shop\n" + strings.Join(lines, "\n") + "\n")
}

// Dump runs pg_dump in custom format.
func Dump(ctx context.Context, dsn string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--no-owner", "--dbname", dsn)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("в образе нет pg_dump — обновите контейнер")
		}
		return nil, fmt.Errorf("pg_dump: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Archive packs the dump and the settings into tar.gz: db.dump, env, README.txt.
func Archive(dump, env []byte, at time.Time) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	readme := "Резервная копия BobrGO Shop от " + at.Format("02.01.2006 15:04 MST") + ".\n" +
		"db.dump — база (pg_restore), env — настройки .env.\n" +
		"Восстановление на сервере: bshop restore <файл" + Ext + ">\n"
	for _, f := range []struct {
		name string
		data []byte
	}{{"db.dump", dump}, {"env", env}, {"README.txt", []byte(readme)}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o600, Size: int64(len(f.data)), ModTime: at}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unpack returns the files of an archive made by Archive.
func Unpack(archive []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(tr, 4<<30))
		if err != nil {
			return nil, err
		}
		out[h.Name] = b
	}
}

// DecryptCommand implements `bshop decrypt-backup`: encrypted file on stdin, tar.gz on stdout,
// password from BSHOP_BACKUP_PASSWORD. It needs no database and no other settings.
func DecryptCommand(stdin io.Reader, stdout io.Writer) error {
	pw := os.Getenv("BSHOP_BACKUP_PASSWORD")
	if pw == "" {
		return errors.New("задайте пароль в BSHOP_BACKUP_PASSWORD")
	}
	file, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	plain, err := Decrypt(pw, file)
	if err != nil {
		return err
	}
	_, err = stdout.Write(plain)
	return err
}
