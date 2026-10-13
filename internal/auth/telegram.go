// Package auth verifies third-party login proofs (Telegram, Yandex ID, VK ID).
// It is stateless: account lookup and sessions live in package service.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Profile is the normalized identity returned by every provider.
type Profile struct {
	Provider  string
	Subject   string // stable provider user id
	Email     string // verified email if the provider gives one
	Username  string
	FirstName string
	LastName  string
	Language  string
	Display   string
	// StartParam is the Mini App start_param (referral / deep link), Telegram only.
	StartParam string
}

var ErrInvalid = errors.New("auth: invalid or expired login data")

// Reasons for a rejected launch, so the cabinet and the log can tell them apart.
var (
	ErrSignature = fmt.Errorf("%w: signature does not match the bot token", ErrInvalid)
	ErrExpired   = fmt.Errorf("%w: auth_date is too old", ErrInvalid)
	ErrClock     = fmt.Errorf("%w: auth_date is in the future (server clock is behind)", ErrInvalid)
)

type tgUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

func tgProfile(u tgUser) *Profile {
	p := &Profile{Provider: "telegram", Subject: strconv.FormatInt(u.ID, 10), Username: u.Username,
		FirstName: u.FirstName, LastName: u.LastName, Language: u.LanguageCode}
	p.Display = strings.TrimSpace(u.FirstName + " " + u.LastName)
	if u.Username != "" {
		p.Display = "@" + u.Username
	}
	return p
}

// VerifyWebAppInitData checks Telegram Mini App initData (https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app).
func VerifyWebAppInitData(initData, botToken string, maxAge time.Duration) (*Profile, error) {
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return nil, ErrInvalid
	}
	hash := vals.Get("hash")
	if hash == "" {
		return nil, ErrInvalid
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	if !hmac.Equal([]byte(hex.EncodeToString(hmacSHA256(secret, []byte(checkString(vals))))), []byte(hash)) {
		return nil, ErrSignature
	}
	if err := freshErr(vals.Get("auth_date"), maxAge); err != nil {
		return nil, err
	}
	var u tgUser
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID == 0 {
		return nil, ErrInvalid
	}
	p := tgProfile(u)
	p.StartParam = vals.Get("start_param")
	return p, nil
}

// VerifyLoginWidget checks data from the Telegram Login Widget (https://core.telegram.org/widgets/login#checking-authorization).
func VerifyLoginWidget(fields map[string]string, botToken string, maxAge time.Duration) (*Profile, error) {
	hash := fields["hash"]
	if hash == "" {
		return nil, ErrInvalid
	}
	vals := url.Values{}
	for k, v := range fields {
		if k != "hash" && v != "" {
			vals.Set(k, v)
		}
	}
	secret := sha256.Sum256([]byte(botToken))
	if !hmac.Equal([]byte(hex.EncodeToString(hmacSHA256(secret[:], []byte(checkString(vals))))), []byte(hash)) {
		return nil, ErrInvalid
	}
	if !fresh(fields["auth_date"], maxAge) {
		return nil, ErrInvalid
	}
	id, err := strconv.ParseInt(fields["id"], 10, 64)
	if err != nil || id == 0 {
		return nil, ErrInvalid
	}
	return tgProfile(tgUser{ID: id, FirstName: fields["first_name"], LastName: fields["last_name"], Username: fields["username"]}), nil
}

func checkString(vals url.Values) string {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + vals.Get(k)
	}
	return strings.Join(parts, "\n")
}

func freshErr(authDate string, maxAge time.Duration) error {
	ts, err := strconv.ParseInt(authDate, 10, 64)
	if err != nil {
		return ErrInvalid
	}
	age := time.Since(time.Unix(ts, 0))
	switch {
	case age < -time.Hour:
		return ErrClock
	case age >= maxAge:
		return ErrExpired
	}
	return nil
}

func fresh(authDate string, maxAge time.Duration) bool {
	ts, err := strconv.ParseInt(authDate, 10, 64)
	if err != nil {
		return false
	}
	age := time.Since(time.Unix(ts, 0))
	return age < maxAge && age > -5*time.Minute
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
