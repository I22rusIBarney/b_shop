// Package moynalog registers self-employed (НПД) income receipts in lknpd.nalog.ru.
package moynalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrAuth      = errors.New("moynalog: authentication failed")
	ErrRetryable = errors.New("moynalog: temporary error")
)

type Client struct {
	base     string
	login    string
	password string
	deviceID string
	http     *http.Client

	mu           sync.Mutex
	token        string
	refreshToken string
	tokenUntil   time.Time
	inn          string
}

func New(base, login, password string) *Client {
	if base == "" {
		base = "https://lknpd.nalog.ru/api/v1"
	}
	return &Client{
		base:     strings.TrimRight(base, "/"),
		login:    login,
		password: password,
		deviceID: randomID(21),
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func randomID(n int) string {
	const a = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = a[int(b[i])%len(a)]
	}
	return string(b)
}

type deviceInfo struct {
	SourceDeviceID string            `json:"sourceDeviceId"`
	SourceType     string            `json:"sourceType"`
	AppVersion     string            `json:"appVersion"`
	MetaDetails    map[string]string `json:"metaDetails"`
}

func (c *Client) device() deviceInfo {
	return deviceInfo{SourceDeviceID: c.deviceID, SourceType: "WEB", AppVersion: "1.0.0",
		MetaDetails: map[string]string{"userAgent": "Mozilla/5.0 (X11; Linux x86_64) bshop"}}
}

type authResp struct {
	Token         string    `json:"token"`
	RefreshToken  string    `json:"refreshToken"`
	TokenExpireIn time.Time `json:"tokenExpireIn"`
	Profile       struct {
		INN string `json:"inn"`
	} `json:"profile"`
}

func (c *Client) post(ctx context.Context, path, token string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %d %s", ErrAuth, resp.StatusCode, trim(data))
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: %d %s", ErrRetryable, resp.StatusCode, trim(data))
	case resp.StatusCode >= 300:
		return fmt.Errorf("moynalog: %d %s", resp.StatusCode, trim(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (c *Client) ensureToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.token != "" && time.Until(c.tokenUntil) > time.Minute {
		return c.token, nil
	}
	var r authResp
	var err error
	if c.refreshToken != "" {
		err = c.post(ctx, "/auth/token", "", map[string]any{"deviceInfo": c.device(), "refreshToken": c.refreshToken}, &r)
	}
	if c.refreshToken == "" || err != nil {
		r = authResp{}
		err = c.post(ctx, "/auth/lkfl", "", map[string]any{"username": c.login, "password": c.password, "deviceInfo": c.device()}, &r)
	}
	if err != nil {
		return "", err
	}
	if r.Token == "" {
		return "", ErrAuth
	}
	c.token = r.Token
	if r.RefreshToken != "" {
		c.refreshToken = r.RefreshToken
	}
	c.tokenUntil = r.TokenExpireIn
	if c.tokenUntil.IsZero() {
		c.tokenUntil = time.Now().Add(30 * time.Minute)
	}
	if r.Profile.INN != "" {
		c.inn = r.Profile.INN
	}
	return c.token, nil
}

func (c *Client) authed(ctx context.Context, path string, body, out any) error {
	tok, err := c.ensureToken(ctx, false)
	if err != nil {
		return err
	}
	err = c.post(ctx, path, tok, body, out)
	if errors.Is(err, ErrAuth) {
		if tok, err = c.ensureToken(ctx, true); err != nil {
			return err
		}
		err = c.post(ctx, path, tok, body, out)
	}
	return err
}

var msk = time.FixedZone("MSK", 3*3600)

func ts(t time.Time) string { return t.In(msk).Format("2006-01-02T15:04:05-07:00") }

// CreateIncome registers a receipt for an individual and returns its UUID.
// operationTime must be the moment the money was received.
func (c *Client) CreateIncome(ctx context.Context, name string, kopecks int64, operationTime time.Time) (string, error) {
	amount := float64(kopecks) / 100
	body := map[string]any{
		"operationTime": ts(operationTime),
		"requestTime":   ts(time.Now()),
		"services":      []map[string]any{{"name": name, "amount": amount, "quantity": 1}},
		"totalAmount":   fmt.Sprintf("%d.%02d", kopecks/100, kopecks%100),
		"client": map[string]any{"contactPhone": nil, "displayName": nil, "inn": nil,
			"incomeType": "FROM_INDIVIDUAL"},
		"paymentType":                     "CASH",
		"ignoreMaxTotalIncomeRestriction": false,
	}
	var r struct {
		ApprovedReceiptUUID string `json:"approvedReceiptUuid"`
	}
	if err := c.authed(ctx, "/income", body, &r); err != nil {
		return "", err
	}
	if r.ApprovedReceiptUUID == "" {
		return "", errors.New("moynalog: empty receipt uuid in response")
	}
	return r.ApprovedReceiptUUID, nil
}

// CancelIncome annuls a receipt (refund).
func (c *Client) CancelIncome(ctx context.Context, receiptUUID, comment string) error {
	if comment == "" {
		comment = "Возврат средств"
	}
	body := map[string]any{
		"operationTime": ts(time.Now()),
		"requestTime":   ts(time.Now()),
		"comment":       comment,
		"receiptUuid":   receiptUUID,
		"partnerCode":   nil,
	}
	return c.authed(ctx, "/cancel", body, nil)
}

// ReceiptURL is the public link to the printable receipt.
func (c *Client) ReceiptURL(ctx context.Context, receiptUUID string) string {
	if _, err := c.ensureToken(ctx, false); err != nil {
		return ""
	}
	c.mu.Lock()
	inn := c.inn
	c.mu.Unlock()
	if inn == "" {
		inn = c.login
	}
	return fmt.Sprintf("%s/receipt/%s/%s/print", c.base, inn, receiptUUID)
}

// Check verifies credentials.
// Reachable checks that the API answers at all, without logging in: the health check runs every minute and
// frequent logins could get the account rate-limited. Any HTTP answer below 500 counts as reachable.
func (c *Client) Reachable(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("moynalog: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) Check(ctx context.Context) error {
	_, err := c.ensureToken(ctx, true)
	return err
}
