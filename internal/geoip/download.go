package geoip

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// FileName is where the downloaded database is kept inside the data directory.
const FileName = "dbip-city-lite.mmdb"

// MaxAge is how old the file may get before a newer monthly release is fetched.
const MaxAge = 32 * 24 * time.Hour

// Stale reports whether the database in dir is missing or older than MaxAge.
func Stale(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, FileName))
	return err != nil || time.Since(st.ModTime()) > MaxAge
}

// Download fetches the current (or, early in a month, the previous) DB-IP City Lite release into dir.
// DB-IP Lite is free under CC BY 4.0: the admin page shows the required attribution.
func Download(ctx context.Context, dir string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var last error
	for _, t := range []time.Time{now, now.AddDate(0, -1, 0)} {
		url := fmt.Sprintf("https://download.db-ip.com/free/dbip-city-lite-%04d-%02d.mmdb.gz", t.Year(), int(t.Month()))
		if err := fetchGz(ctx, url, filepath.Join(dir, FileName)); err != nil {
			last = err
			continue
		}
		return nil
	}
	return last
}

func fetchGz(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("geoip: %s: %s", url, resp.Status)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer zr.Close()
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	// The release is about 130 MB unpacked; refuse anything absurd.
	n, err := io.Copy(f, io.LimitReader(zr, 1<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n < 1<<20 {
		err = fmt.Errorf("geoip: %s: file too small (%d bytes)", url, n)
	}
	if err == nil {
		var r *Reader
		if r, err = Open(tmp); err == nil {
			_ = r.Close()
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
