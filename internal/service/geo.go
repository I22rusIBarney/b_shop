package service

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bshop/internal/geoip"
	"bshop/internal/remnawave"
)

// Geography of subscription requests.
//
// Every time a client's VPN app refreshes its subscription the panel records the IP address in its request
// history. The shop copies new records into sub_requests, resolves country and city once (DB-IP City Lite,
// downloaded into the data directory and refreshed monthly) and shows aggregates on a globe/map. The numbers
// say where subscriptions are fetched from, not where people physically are: a client who refreshes through
// another VPN shows up in that VPN's country.

// requestHistory is implemented by the real panel client; test doubles of Panel don't need it.
type requestHistory interface {
	SubscriptionRequests(ctx context.Context, start, size int) ([]remnawave.SubRequest, int, error)
}

// geoState is the runtime state of the feature (guarded by Service.geoMu).
type geoState struct {
	db        geoip.Lookuper
	dbAt      time.Time // modification time of the loaded file
	dbErr     string
	dlErr     string // last download failure; retried after a few hours
	dlAt      time.Time
	cursor    int64 // newest panel record seen, so records of unknown users are not rescanned every run
	syncAt    time.Time
	syncErr   string
	syncAdded int
}

// geoDir is where the geolocation database lives: GEOIP_DIR, else /data/geoip (a volume in docker-compose), else a temp dir.
func geoDir() string {
	if d := os.Getenv("GEOIP_DIR"); d != "" {
		return d
	}
	for _, d := range []string{"/data/geoip", filepath.Join(os.TempDir(), "bshop-geoip")} {
		if err := os.MkdirAll(d, 0o755); err == nil {
			if f, err := os.CreateTemp(d, ".w"); err == nil {
				name := f.Name()
				_ = f.Close()
				_ = os.Remove(name)
				return d
			}
		}
	}
	return os.TempDir()
}

// geoReader returns the loaded database, fetching or refreshing the file when needed. Nil when unavailable.
func (s *Service) geoReader(ctx context.Context) geoip.Lookuper {
	if s.GeoOverride != nil {
		return s.GeoOverride
	}
	dir := geoDir()
	path := filepath.Join(dir, geoip.FileName)

	s.geoLoad.Lock() // one download/open at a time; geoMu is only held briefly so the admin page never waits for a download
	defer s.geoLoad.Unlock()
	st, statErr := os.Stat(path)
	stale := statErr != nil || time.Since(st.ModTime()) > geoip.MaxAge
	s.geoMu.Lock()
	retryOK := s.geo.dlErr == "" || time.Since(s.geo.dlAt) > 6*time.Hour
	s.geoMu.Unlock()
	if stale && retryOK {
		s.Log.Info("geoip: downloading DB-IP City Lite")
		err := geoip.Download(ctx, dir, time.Now().UTC())
		s.geoMu.Lock()
		s.geo.dlAt = time.Now()
		if err != nil {
			s.geo.dlErr = "не удалось скачать базу: " + err.Error()
			s.Log.Warn("geoip download failed", "err", err)
		} else {
			s.geo.dlErr = ""
			s.geo.db = nil // reload the new file
		}
		s.geoMu.Unlock()
		st, statErr = os.Stat(path)
	}
	if statErr != nil {
		return nil
	}
	s.geoMu.Lock()
	cur, curAt := s.geo.db, s.geo.dbAt
	s.geoMu.Unlock()
	if cur != nil && st.ModTime().Equal(curAt) {
		return cur
	}
	r, err := geoip.Open(path)
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	if err != nil {
		s.geo.dbErr = "база повреждена: " + err.Error()
		return nil
	}
	s.geo.dbErr = ""
	s.geo.db, s.geo.dbAt = r, st.ModTime()
	return r
}

// SyncGeo copies new records of the panel's request history into sub_requests. Runs every few minutes.
func (s *Service) SyncGeo(ctx context.Context) {
	cfg := s.set().Geo
	if !cfg.Enabled || s.Panel == nil {
		return
	}
	rh, ok := s.Panel.(requestHistory)
	if !ok {
		return
	}
	added, newest, err := s.syncGeo(ctx, rh, cfg.RetentionDays)
	s.geoMu.Lock()
	if err == nil && newest > s.geo.cursor {
		s.geo.cursor = newest
	}
	s.geo.syncAt = time.Now()
	s.geo.syncAdded = added
	s.geo.syncErr = ""
	if err != nil {
		s.geo.syncErr = err.Error()
		s.Log.Warn("geo sync", "err", err)
	}
	s.geoMu.Unlock()
}

func (s *Service) syncGeo(ctx context.Context, rh requestHistory, retentionDays int) (added int, newest int64, err error) {
	db := s.geoReader(ctx)
	if db == nil {
		return 0, 0, errors.New("база геолокации ещё не загружена")
	}
	type owner struct{ cust, sub int64 }
	owners := map[int64]owner{}
	rows, err := s.Store.Pool.Query(ctx, `SELECT rw_user_id, customer_id, id FROM subscriptions WHERE rw_user_id IS NOT NULL ORDER BY is_primary, id`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var rw, c, sid int64
		if err := rows.Scan(&rw, &c, &sid); err != nil {
			rows.Close()
			return 0, 0, err
		}
		owners[rw] = owner{c, sid}
	}
	rows.Close()
	if len(owners) == 0 {
		return 0, 0, nil
	}
	var maxID int64
	if err := s.Store.Pool.QueryRow(ctx, `SELECT coalesce(max(panel_id), 0) FROM sub_requests`).Scan(&maxID); err != nil {
		return 0, 0, err
	}
	s.geoMu.Lock()
	if s.geo.cursor > maxID {
		maxID = s.geo.cursor
	}
	s.geoMu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	const size, maxPages = 500, 40
	first, total, err := rh.SubscriptionRequests(ctx, 0, size)
	if err != nil {
		return 0, 0, err
	}
	// Normally the newest records come first. If a panel version returns them oldest first, walk the pages from the end.
	asc := len(first) > 1 && first[0].RequestedAt.Before(first[len(first)-1].RequestedAt) && total > size
	page := first
	for n := 0; n < maxPages; n++ {
		if n > 0 {
			start := n * size
			if asc {
				start = ((total-1)/size - n) * size
				if start < 0 {
					break
				}
			}
			if page, _, err = rh.SubscriptionRequests(ctx, start, size); err != nil {
				return added, newest, err
			}
		} else if asc {
			start := ((total - 1) / size) * size
			if page, _, err = rh.SubscriptionRequests(ctx, start, size); err != nil {
				return added, newest, err
			}
		}
		if len(page) == 0 {
			break
		}
		fresh := false
		for _, rec := range page {
			if rec.ID > newest {
				newest = rec.ID
			}
			if rec.ID > maxID && rec.RequestedAt.After(cutoff) {
				fresh = true
			} else {
				continue
			}
			o, known := owners[rec.UserID]
			if !known || rec.RequestIP == nil {
				continue
			}
			ip, err := netip.ParseAddr(strings.TrimSpace(*rec.RequestIP))
			if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
				continue
			}
			ip = ip.Unmap()
			loc, _ := db.Lookup(ip)
			ua := ""
			if rec.UserAgent != nil {
				ua = *rec.UserAgent
				if r := []rune(ua); len(r) > 300 {
					ua = string(r[:300])
				}
			}
			var lat, lon *float64
			if loc.HasGeo {
				lat, lon = &loc.Lat, &loc.Lon
			}
			tag, err := s.Store.Pool.Exec(ctx, `
				INSERT INTO sub_requests (panel_id, customer_id, subscription_id, ip, country, region, city, lat, lon, user_agent, requested_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT (panel_id) DO NOTHING`,
				rec.ID, o.cust, o.sub, ip.String(), loc.Country, loc.Region, loc.City, lat, lon, ua, rec.RequestedAt)
			if err != nil {
				return added, newest, err
			}
			added += int(tag.RowsAffected())
		}
		if !fresh {
			break // a whole page of records we already have (or that are past the retention period)
		}
	}
	return added, newest, nil
}

// CleanGeo deletes requests older than the retention period.
func (s *Service) CleanGeo(ctx context.Context) {
	days := s.set().Geo.RetentionDays
	if days < 7 {
		days = 90
	}
	_, _ = s.Store.Pool.Exec(ctx, `DELETE FROM sub_requests WHERE requested_at < now() - make_interval(days => $1)`, days)
}

// ---- reports ----

type GeoStatus struct {
	Enabled   bool       `json:"enabled"`
	Ready     bool       `json:"ready"` // the geolocation database is loaded
	DBDate    *time.Time `json:"db_date,omitempty"`
	DBError   string     `json:"db_error,omitempty"`
	SyncedAt  *time.Time `json:"synced_at,omitempty"`
	SyncError string     `json:"sync_error,omitempty"`
	Retention int        `json:"retention_days"`
}

type GeoCity struct {
	City   string   `json:"city"`
	Region string   `json:"region,omitempty"`
	Count  int64    `json:"count"`
	Lat    *float64 `json:"lat,omitempty"`
	Lon    *float64 `json:"lon,omitempty"`
}

type GeoCountry struct {
	Code      string    `json:"code"` // ISO alpha-2; "" = unknown
	Count     int64     `json:"count"`
	Customers int64     `json:"customers"`
	Cities    []GeoCity `json:"cities"`
}

type GeoReport struct {
	From      time.Time    `json:"from"`
	To        time.Time    `json:"to"`
	Total     int64        `json:"total"`
	Customers int64        `json:"customers"`
	Countries []GeoCountry `json:"countries"`
	Status    GeoStatus    `json:"status"`
}

func (s *Service) geoStatus() GeoStatus {
	cfg := s.set().Geo
	st := GeoStatus{Enabled: cfg.Enabled, Retention: cfg.RetentionDays}
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	st.Ready = s.geo.db != nil || s.GeoOverride != nil
	if !s.geo.dbAt.IsZero() {
		t := s.geo.dbAt
		st.DBDate = &t
	}
	st.DBError = s.geo.dbErr
	if st.DBError == "" {
		st.DBError = s.geo.dlErr
	}
	if !s.geo.syncAt.IsZero() {
		t := s.geo.syncAt
		st.SyncedAt = &t
	}
	st.SyncError = s.geo.syncErr
	return st
}

// warmGeoDB starts loading (or downloading) the geolocation database in the background when it isn't loaded yet,
// so the admin page doesn't wait for the next sync run and never blocks on a download.
func (s *Service) warmGeoDB() {
	if !s.set().Geo.Enabled || s.GeoOverride != nil {
		return
	}
	s.geoMu.Lock()
	ready := s.geo.db != nil
	s.geoMu.Unlock()
	if ready {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		s.geoReader(ctx)
	}()
}

// geoAggregate groups requests matching where (with args) by country and city.
func (s *Service) geoAggregate(ctx context.Context, where string, args ...any) ([]GeoCountry, int64, int64, error) {
	rows, err := s.Store.Pool.Query(ctx, `
		SELECT country, region, city, count(*), avg(lat), avg(lon)
		FROM sub_requests WHERE `+where+` GROUP BY country, region, city`, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	byCountry := map[string]*GeoCountry{}
	var total int64
	for rows.Next() {
		var cc, region, city string
		var n int64
		var lat, lon *float64
		if err := rows.Scan(&cc, &region, &city, &n, &lat, &lon); err != nil {
			rows.Close()
			return nil, 0, 0, err
		}
		c := byCountry[cc]
		if c == nil {
			c = &GeoCountry{Code: cc, Cities: []GeoCity{}}
			byCountry[cc] = c
		}
		c.Count += n
		total += n
		if city != "" {
			c.Cities = append(c.Cities, GeoCity{City: city, Region: region, Count: n, Lat: lat, Lon: lon})
		}
	}
	rows.Close()
	crows, err := s.Store.Pool.Query(ctx, `SELECT country, count(DISTINCT customer_id) FROM sub_requests WHERE `+where+` GROUP BY country`, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	for crows.Next() {
		var cc string
		var n int64
		if err := crows.Scan(&cc, &n); err != nil {
			crows.Close()
			return nil, 0, 0, err
		}
		if c := byCountry[cc]; c != nil {
			c.Customers = n
		}
	}
	crows.Close()
	var customers int64
	if err := s.Store.Pool.QueryRow(ctx, `SELECT count(DISTINCT customer_id) FROM sub_requests WHERE `+where, args...).Scan(&customers); err != nil {
		return nil, 0, 0, err
	}
	out := make([]GeoCountry, 0, len(byCountry))
	for _, c := range byCountry {
		sort.Slice(c.Cities, func(i, j int) bool {
			if c.Cities[i].Count != c.Cities[j].Count {
				return c.Cities[i].Count > c.Cities[j].Count
			}
			return c.Cities[i].City < c.Cities[j].City
		})
		if len(c.Cities) > 40 {
			c.Cities = c.Cities[:40]
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	return out, total, customers, nil
}

// StatsGeo is the shop-wide report for the period (7d, 1m, 3m, … as in the other statistics).
func (s *Service) StatsGeo(ctx context.Context, period string) (*GeoReport, error) {
	s.warmGeoDB()
	r := s.statsRange(period, "")
	countries, total, customers, err := s.geoAggregate(ctx, `requested_at >= $1 AND requested_at < $2`, r.From, r.To)
	if err != nil {
		return nil, err
	}
	return &GeoReport{From: r.From, To: r.To, Total: total, Customers: customers, Countries: countries, Status: s.geoStatus()}, nil
}

type GeoRequest struct {
	At        time.Time `json:"at"`
	IP        string    `json:"ip"`
	Country   string    `json:"country"`
	Region    string    `json:"region,omitempty"`
	City      string    `json:"city,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
}

type GeoDevice struct {
	Name     string    `json:"name"`
	Platform string    `json:"platform,omitempty"`
	IP       string    `json:"ip"`
	Country  string    `json:"country"`
	Region   string    `json:"region,omitempty"`
	City     string    `json:"city,omitempty"`
	At       time.Time `json:"at"`
}

type CustomerGeoReport struct {
	Total     int64        `json:"total"`
	Countries []GeoCountry `json:"countries"`
	Recent    []GeoRequest `json:"recent"`
	Devices   []GeoDevice  `json:"devices"`
	Status    GeoStatus    `json:"status"`
}

// CustomerGeo is the same picture for one customer: their subscription requests plus the last IP of each device.
func (s *Service) CustomerGeo(ctx context.Context, customerID int64) (*CustomerGeoReport, error) {
	countries, total, _, err := s.geoAggregate(ctx, `customer_id = $1`, customerID)
	if err != nil {
		return nil, err
	}
	rep := &CustomerGeoReport{Total: total, Countries: countries, Recent: []GeoRequest{}, Devices: []GeoDevice{}, Status: s.geoStatus()}
	rows, err := s.Store.Pool.Query(ctx, `
		SELECT requested_at, host(ip), country, region, city, user_agent FROM sub_requests
		WHERE customer_id = $1 ORDER BY requested_at DESC LIMIT 25`, customerID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g GeoRequest
		if err := rows.Scan(&g.At, &g.IP, &g.Country, &g.Region, &g.City, &g.UserAgent); err != nil {
			rows.Close()
			return nil, err
		}
		rep.Recent = append(rep.Recent, g)
	}
	rows.Close()

	// Devices come straight from the panel (best effort: the page works without them).
	if s.Panel == nil {
		return rep, nil
	}
	db := s.geoReader(ctx)
	srows, err := s.Store.Pool.Query(ctx, `SELECT id, rw_user_id FROM subscriptions WHERE customer_id = $1 AND rw_user_id IS NOT NULL ORDER BY id`, customerID)
	if err != nil {
		return rep, nil
	}
	type sub struct{ id, rw int64 }
	var subs []sub
	for srows.Next() {
		var x sub
		if srows.Scan(&x.id, &x.rw) == nil {
			subs = append(subs, x)
		}
	}
	srows.Close()
	pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	for _, x := range subs {
		devs, err := s.Panel.UserDevices(pctx, x.rw)
		if err != nil {
			continue
		}
		names := map[string]string{}
		if nr, err := s.Store.Pool.Query(ctx, `SELECT hwid, name FROM device_names WHERE subscription_id = $1`, x.id); err == nil {
			for nr.Next() {
				var h, n string
				if nr.Scan(&h, &n) == nil {
					names[h] = n
				}
			}
			nr.Close()
		}
		for _, d := range devs {
			if d.RequestIP == nil {
				continue
			}
			ip, err := netip.ParseAddr(strings.TrimSpace(*d.RequestIP))
			if err != nil {
				continue
			}
			gd := GeoDevice{IP: ip.Unmap().String(), At: d.UpdatedAt, Name: names[d.HWID]}
			if gd.Name == "" {
				gd.Name = geoDeviceTitle(d)
			}
			if d.Platform != nil {
				gd.Platform = *d.Platform
			}
			if db != nil {
				if loc, ok := db.Lookup(ip); ok {
					gd.Country, gd.Region, gd.City = loc.Country, loc.Region, loc.City
				}
			}
			rep.Devices = append(rep.Devices, gd)
		}
	}
	sort.Slice(rep.Devices, func(i, j int) bool { return rep.Devices[i].At.After(rep.Devices[j].At) })
	return rep, nil
}

func geoDeviceTitle(d remnawave.Device) string {
	var parts []string
	for _, p := range []*string{d.DeviceModel, d.Platform} {
		if p != nil && strings.TrimSpace(*p) != "" {
			parts = append(parts, strings.TrimSpace(*p))
		}
	}
	if len(parts) == 0 {
		return "Устройство " + maskHWID(d.HWID)
	}
	return strings.Join(parts, " · ")
}
