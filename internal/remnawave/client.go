// Package remnawave is a small client for the Remnawave panel API (v3.x, numeric user ids).
// It knows nothing about shop settings: callers pass every value explicitly.
package remnawave

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("remnawave: not found")

type Client struct {
	base   string
	token  string
	local  bool
	cookie string
	http   *http.Client
}

type Options struct {
	// Local adds X-Forwarded-* headers required when talking to the panel over plain HTTP in Docker.
	Local  bool
	Cookie string
}

func New(baseURL, token string, opt Options) *Client {
	return &Client{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		local:  opt.Local,
		cookie: opt.Cookie,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

type APIError struct {
	Status  int
	Message string
	Code    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("remnawave: %d %s (%s)", e.Status, e.Message, e.Code)
	}
	return fmt.Sprintf("remnawave: %d %s", e.Status, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.local {
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("remnawave %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Message   string `json:"message"`
			ErrorCode string `json:"errorCode"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(data))
			if len(e.Message) > 300 {
				e.Message = e.Message[:300]
			}
		}
		return &APIError{Status: resp.StatusCode, Message: e.Message, Code: e.ErrorCode}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("remnawave %s %s: decode: %w", method, path, err)
	}
	return nil
}

type envelope[T any] struct {
	Response T `json:"response"`
}

// ---- Users ----

type UserTraffic struct {
	UsedTrafficBytes         int64      `json:"usedTrafficBytes"`
	LifetimeUsedTrafficBytes int64      `json:"lifetimeUsedTrafficBytes"`
	OnlineAt                 *time.Time `json:"onlineAt"`
	FirstConnectedAt         *time.Time `json:"firstConnectedAt"`
	LastConnectedNodeUUID    *uuid.UUID `json:"lastConnectedNodeUuid"`
}

type Squad struct {
	UUID uuid.UUID `json:"uuid"`
	Name string    `json:"name"`
	// External squads only: the subscription page config assigned to the squad (nil = panel default).
	SubpageConfigUUID *uuid.UUID `json:"subpageConfigUuid,omitempty"`
}

type User struct {
	ID                   int64       `json:"id"`
	ShortUUID            string      `json:"shortUuid"`
	Username             string      `json:"username"`
	Status               string      `json:"status"`
	TrafficLimitBytes    int64       `json:"trafficLimitBytes"`
	TrafficLimitStrategy string      `json:"trafficLimitStrategy"`
	ExpireAt             time.Time   `json:"expireAt"`
	TelegramID           *int64      `json:"telegramId"`
	Email                *string     `json:"email"`
	Description          *string     `json:"description"`
	Tag                  *string     `json:"tag"`
	HwidDeviceLimit      *int        `json:"hwidDeviceLimit"`
	ExternalSquadUUID    *uuid.UUID  `json:"externalSquadUuid"`
	SubRevokedAt         *time.Time  `json:"subRevokedAt"`
	LastTrafficResetAt   *time.Time  `json:"lastTrafficResetAt"`
	CreatedAt            time.Time   `json:"createdAt"`
	SubscriptionURL      string      `json:"subscriptionUrl"`
	ActiveInternalSquads []Squad     `json:"activeInternalSquads"`
	UserTraffic          UserTraffic `json:"userTraffic"`
}

// CreateUser is the body for POST /api/users.
type CreateUser struct {
	Username             string      `json:"username"`
	Status               string      `json:"status,omitempty"`
	ExpireAt             time.Time   `json:"expireAt"`
	TrafficLimitBytes    int64       `json:"trafficLimitBytes"`
	TrafficLimitStrategy string      `json:"trafficLimitStrategy,omitempty"`
	Description          string      `json:"description,omitempty"`
	Tag                  *string     `json:"tag,omitempty"`
	TelegramID           *int64      `json:"telegramId,omitempty"`
	Email                *string     `json:"email,omitempty"`
	HwidDeviceLimit      *int        `json:"hwidDeviceLimit,omitempty"`
	ActiveInternalSquads []uuid.UUID `json:"activeInternalSquads"`
	ExternalSquadUUID    *uuid.UUID  `json:"externalSquadUuid,omitempty"`
}

// UpdateUser is the body for PATCH /api/users; nil fields are left unchanged.
// Use ClearTag / ClearExternalSquad to explicitly null a value.
type UpdateUser struct {
	ID                   int64       `json:"id"`
	Status               string      `json:"status,omitempty"`
	ExpireAt             *time.Time  `json:"expireAt,omitempty"`
	TrafficLimitBytes    *int64      `json:"trafficLimitBytes,omitempty"`
	TrafficLimitStrategy string      `json:"trafficLimitStrategy,omitempty"`
	Description          *string     `json:"description,omitempty"`
	Tag                  *string     `json:"-"`
	TelegramID           *int64      `json:"telegramId,omitempty"`
	Email                *string     `json:"email,omitempty"`
	HwidDeviceLimit      *int        `json:"hwidDeviceLimit,omitempty"`
	ActiveInternalSquads []uuid.UUID `json:"activeInternalSquads,omitempty"`
	ExternalSquadUUID    *uuid.UUID  `json:"-"`
	ClearTag             bool        `json:"-"`
	ClearExternalSquad   bool        `json:"-"`
}

func (u UpdateUser) MarshalJSON() ([]byte, error) {
	type plain UpdateUser
	m := map[string]any{}
	b, err := json.Marshal(plain(u))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	switch {
	case u.ClearTag:
		m["tag"] = nil
	case u.Tag != nil:
		m["tag"] = *u.Tag
	}
	switch {
	case u.ClearExternalSquad:
		m["externalSquadUuid"] = nil
	case u.ExternalSquadUUID != nil:
		m["externalSquadUuid"] = u.ExternalSquadUUID.String()
	}
	return json.Marshal(m)
}

func (c *Client) CreateUser(ctx context.Context, in CreateUser) (*User, error) {
	if in.ActiveInternalSquads == nil {
		in.ActiveInternalSquads = []uuid.UUID{}
	}
	var out envelope[User]
	if err := c.do(ctx, http.MethodPost, "/api/users", in, &out); err != nil {
		return nil, err
	}
	return &out.Response, nil
}

func (c *Client) UpdateUser(ctx context.Context, in UpdateUser) (*User, error) {
	var out envelope[User]
	if err := c.do(ctx, http.MethodPatch, "/api/users", in, &out); err != nil {
		return nil, err
	}
	return &out.Response, nil
}

func (c *Client) GetUser(ctx context.Context, id int64) (*User, error) {
	var out envelope[User]
	if err := c.do(ctx, http.MethodGet, "/api/users/"+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out.Response, nil
}

func (c *Client) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	var out envelope[User]
	if err := c.do(ctx, http.MethodGet, "/api/users/by-username/"+url.PathEscape(username), nil, &out); err != nil {
		return nil, err
	}
	return &out.Response, nil
}

func (c *Client) GetUserByShortUUID(ctx context.Context, short string) (*User, error) {
	var out envelope[User]
	if err := c.do(ctx, http.MethodGet, "/api/users/by-short-uuid/"+url.PathEscape(short), nil, &out); err != nil {
		return nil, err
	}
	return &out.Response, nil
}

// ListUsers pages through every panel user.
func (c *Client) ListUsers(ctx context.Context, fn func([]User) error) error {
	const size = 500
	for start := 0; ; start += size {
		var out envelope[struct {
			Users []User `json:"users"`
			Total int    `json:"total"`
		}]
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/users?size=%d&start=%d", size, start), nil, &out); err != nil {
			return err
		}
		if len(out.Response.Users) > 0 {
			if err := fn(out.Response.Users); err != nil {
				return err
			}
		}
		if len(out.Response.Users) < size || start+size >= out.Response.Total {
			return nil
		}
	}
}

func (c *Client) DeleteUser(ctx context.Context, id int64) error {
	err := c.do(ctx, http.MethodDelete, "/api/users/"+strconv.FormatInt(id, 10), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) action(ctx context.Context, id int64, act string, body any) (*User, error) {
	var out envelope[User]
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/users/%d/actions/%s", id, act), body, &out); err != nil {
		return nil, err
	}
	return &out.Response, nil
}

func (c *Client) EnableUser(ctx context.Context, id int64) (*User, error) {
	return c.action(ctx, id, "enable", nil)
}

func (c *Client) DisableUser(ctx context.Context, id int64) (*User, error) {
	return c.action(ctx, id, "disable", nil)
}

func (c *Client) ResetTraffic(ctx context.Context, id int64) (*User, error) {
	return c.action(ctx, id, "reset-traffic", nil)
}

// RevokeSubscription issues new credentials; onlyPasswords keeps the subscription link.
func (c *Client) RevokeSubscription(ctx context.Context, id int64, onlyPasswords bool) (*User, error) {
	return c.action(ctx, id, "revoke", map[string]bool{"revokeOnlyPasswords": onlyPasswords})
}

// ---- HWID devices ----

type Device struct {
	HWID        string    `json:"hwid"`
	UserID      int64     `json:"userId"`
	Platform    *string   `json:"platform"`
	OSVersion   *string   `json:"osVersion"`
	DeviceModel *string   `json:"deviceModel"`
	UserAgent   *string   `json:"userAgent"`
	RequestIP   *string   `json:"requestIp"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type devicesResp struct {
	Total   int      `json:"total"`
	Devices []Device `json:"devices"`
}

func (c *Client) UserDevices(ctx context.Context, userID int64) ([]Device, error) {
	var out envelope[devicesResp]
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/hwid/devices/%d", userID), nil, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return out.Response.Devices, nil
}

func (c *Client) DeleteDevice(ctx context.Context, userID int64, hwid string) error {
	return c.do(ctx, http.MethodPost, "/api/hwid/devices/delete", map[string]any{"userId": userID, "hwid": hwid}, nil)
}

func (c *Client) DeleteAllDevices(ctx context.Context, userID int64) error {
	return c.do(ctx, http.MethodPost, "/api/hwid/devices/delete-all", map[string]any{"userId": userID}, nil)
}

// ---- Squads, nodes, health ----

func (c *Client) InternalSquads(ctx context.Context) ([]Squad, error) {
	var out envelope[struct {
		InternalSquads []Squad `json:"internalSquads"`
	}]
	if err := c.do(ctx, http.MethodGet, "/api/internal-squads", nil, &out); err != nil {
		return nil, err
	}
	return out.Response.InternalSquads, nil
}

func (c *Client) ExternalSquads(ctx context.Context) ([]Squad, error) {
	var out envelope[struct {
		ExternalSquads []Squad `json:"externalSquads"`
	}]
	if err := c.do(ctx, http.MethodGet, "/api/external-squads", nil, &out); err != nil {
		return nil, err
	}
	return out.Response.ExternalSquads, nil
}

type Node struct {
	UUID        uuid.UUID `json:"uuid"`
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	CountryCode string    `json:"countryCode"`
	IsConnected bool      `json:"isConnected"`
	IsDisabled  bool      `json:"isDisabled"`
}

func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var out envelope[[]Node]
	if err := c.do(ctx, http.MethodGet, "/api/nodes", nil, &out); err != nil {
		return nil, err
	}
	return out.Response, nil
}

func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/api/system/health", nil, nil)
}

// ---- Subscription page configs (apps and installation guides) ----

type SubpageConfigInfo struct {
	UUID         uuid.UUID `json:"uuid"`
	Name         string    `json:"name"`
	ViewPosition int       `json:"viewPosition"`
}

func (c *Client) SubpageConfigs(ctx context.Context) ([]SubpageConfigInfo, error) {
	var out envelope[struct {
		Configs []SubpageConfigInfo `json:"configs"`
	}]
	if err := c.do(ctx, http.MethodGet, "/api/subscription-page-configs", nil, &out); err != nil {
		return nil, err
	}
	return out.Response.Configs, nil
}

// SubpageConfig returns the raw config JSON (platforms, apps, blocks, svgLibrary...).
func (c *Client) SubpageConfig(ctx context.Context, id uuid.UUID) (json.RawMessage, error) {
	var out envelope[struct {
		Config json.RawMessage `json:"config"`
	}]
	if err := c.do(ctx, http.MethodGet, "/api/subscription-page-configs/"+id.String(), nil, &out); err != nil {
		return nil, err
	}
	return out.Response.Config, nil
}
