// Package settings keeps every shop setting in the database, editable from the admin panel.
// Each section is a JSON document under its own key; fields tagged secret:"true" are
// encrypted at rest and masked when shown to the admin UI.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"bshop/internal/secrets"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Mask is what the admin UI receives instead of a configured secret.
// Sending it back unchanged keeps the stored value.
const Mask = "••••••••"

type Store struct {
	pool *pgxpool.Pool
	box  *secrets.Box
	cur  atomic.Pointer[All]
	mu   sync.Mutex // serializes writes

	listenersMu sync.Mutex
	listeners   []func(*All)
}

func NewStore(pool *pgxpool.Pool, box *secrets.Box) *Store {
	s := &Store{pool: pool, box: box}
	d := Defaults()
	s.cur.Store(&d)
	return s
}

// Get returns the current snapshot. Callers must not modify it.
func (s *Store) Get() *All { return s.cur.Load() }

// OnChange registers a callback invoked after every successful update.
func (s *Store) OnChange(fn func(*All)) {
	s.listenersMu.Lock()
	s.listeners = append(s.listeners, fn)
	s.listenersMu.Unlock()
}

// Load reads all sections from the database on top of the defaults.
func (s *Store) Load(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return err
	}
	defer rows.Close()
	all := Defaults()
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return err
		}
		sec := sectionPtr(&all, key)
		if sec == nil {
			continue // section from a newer/older version
		}
		if err := json.Unmarshal(raw, sec); err != nil {
			return fmt.Errorf("settings %s: %w", key, err)
		}
		if err := s.walkSecrets(sec, s.box.Decrypt); err != nil {
			return fmt.Errorf("settings %s: %w", key, err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.cur.Store(&all)
	return nil
}

// Sections lists the section keys known to this version.
func Sections() []string {
	t := reflect.TypeOf(All{})
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out = append(out, jsonName(t.Field(i)))
	}
	return out
}

// Public returns a section as JSON for the admin UI with secrets masked.
func (s *Store) Public(key string) (json.RawMessage, error) {
	cp := *s.Get()
	sec := sectionPtr(&cp, key)
	if sec == nil {
		return nil, ErrUnknownSection
	}
	clone := reflect.New(reflect.TypeOf(sec).Elem())
	clone.Elem().Set(reflect.ValueOf(sec).Elem())
	_ = s.walkSecrets(clone.Interface(), func(v string) (string, error) {
		if v == "" {
			return "", nil
		}
		return Mask, nil
	})
	return json.Marshal(clone.Interface())
}

var ErrUnknownSection = errors.New("unknown settings section")

// Validator is implemented by sections that check their own values.
type Validator interface{ Validate() error }

// Update replaces a section with the given JSON. Masked secrets keep their stored value.
func (s *Store) Update(ctx context.Context, key string, raw json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := *s.Get()
	sec := sectionPtr(&next, key)
	if sec == nil {
		return ErrUnknownSection
	}
	old := reflect.New(reflect.TypeOf(sec).Elem())
	old.Elem().Set(reflect.ValueOf(sec).Elem())

	fresh := reflect.New(reflect.TypeOf(sec).Elem())
	fresh.Elem().Set(reflect.ValueOf(sectionPtr(ptrDefaults(), key)).Elem())
	if err := json.Unmarshal(raw, fresh.Interface()); err != nil {
		return fmt.Errorf("invalid %s settings: %w", key, err)
	}
	restoreMasked(fresh.Elem(), old.Elem())
	if v, ok := fresh.Interface().(Validator); ok {
		if err := v.Validate(); err != nil {
			return err
		}
	}
	reflect.ValueOf(sec).Elem().Set(fresh.Elem())

	// Persist with secrets encrypted.
	stored := reflect.New(fresh.Elem().Type())
	stored.Elem().Set(fresh.Elem())
	if err := s.walkSecrets(stored.Interface(), s.box.Encrypt); err != nil {
		return err
	}
	body, err := json.Marshal(stored.Interface())
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, body); err != nil {
		return err
	}
	s.cur.Store(&next)
	s.listenersMu.Lock()
	ls := append([]func(*All){}, s.listeners...)
	s.listenersMu.Unlock()
	for _, fn := range ls {
		fn(&next)
	}
	return nil
}

func ptrDefaults() *All { d := Defaults(); return &d }

func sectionPtr(all *All, key string) any {
	v := reflect.ValueOf(all).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if jsonName(t.Field(i)) == key {
			return v.Field(i).Addr().Interface()
		}
	}
	return nil
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// walkSecrets applies fn to every string field tagged secret:"true", recursing into structs and slices.
func (s *Store) walkSecrets(ptr any, fn func(string) (string, error)) error {
	return walk(reflect.ValueOf(ptr).Elem(), fn)
}

func walk(v reflect.Value, fn func(string) (string, error)) error {
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := v.Field(i)
			if !f.CanSet() {
				continue
			}
			if t.Field(i).Tag.Get("secret") == "true" && f.Kind() == reflect.String {
				nv, err := fn(f.String())
				if err != nil {
					return err
				}
				f.SetString(nv)
				continue
			}
			if err := walk(f, fn); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if err := walk(v.Index(i), fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// restoreMasked copies old secret values into fresh wherever the UI sent the mask back.
func restoreMasked(fresh, old reflect.Value) {
	if fresh.Kind() != reflect.Struct {
		return
	}
	t := fresh.Type()
	for i := 0; i < t.NumField(); i++ {
		f := fresh.Field(i)
		if !f.CanSet() {
			continue
		}
		if t.Field(i).Tag.Get("secret") == "true" && f.Kind() == reflect.String {
			if f.String() == Mask {
				f.SetString(old.Field(i).String())
			}
			continue
		}
		if f.Kind() == reflect.Struct {
			restoreMasked(f, old.Field(i))
		}
	}
}
