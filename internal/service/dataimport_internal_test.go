package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackDump(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gz := func(b []byte) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		w.Write(b)
		w.Close()
		return buf.Bytes()
	}
	tarOf := func(files map[string][]byte) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for n, b := range files {
			tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg})
			tw.Write(b)
		}
		tw.Close()
		return buf.Bytes()
	}
	sql := []byte("--\n-- PostgreSQL database dump\n--\nSET statement_timeout = 0;\n")
	custom := append([]byte("PGDMP"), make([]byte, 100)...)
	cases := []struct {
		name, kind string
		data       []byte
	}{
		{"a.sql", "sql", sql},
		{"a.dump", "custom", custom},
		{"a.sql.gz", "sql", gz(sql)},
		{"backup.tar.gz", "custom", gz(tarOf(map[string][]byte{"backup/.env": []byte("X=1"), "backup/db.dump": custom}))},
		{"backup.tar", "sql", tarOf(map[string][]byte{"dump/rwp.sql.gz": gz(sql)})},
	}
	for _, c := range cases {
		_, kind, err := unpackDump(write(c.name, c.data), func(string) {})
		if err != nil || kind != c.kind {
			t.Errorf("%s: kind=%q err=%v", c.name, kind, err)
		}
	}
	if _, _, err := unpackDump(write("x.jpg", []byte("\xff\xd8\xff garbage")), func(string) {}); err == nil {
		t.Error("garbage accepted")
	}
}
