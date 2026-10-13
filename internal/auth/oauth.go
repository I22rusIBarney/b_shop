package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

// RandomToken returns a URL-safe random string with n bytes of entropy.
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// PKCEChallenge returns the S256 challenge for a verifier.
func PKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func postForm(ctx context.Context, endpoint string, form url.Values, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doJSON(req, out)
}

func doJSON(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %d %s", req.URL.Host, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, out)
}

// ---- Yandex ID ----

type Yandex struct {
	ClientID, Secret, RedirectURI string
}

func (y Yandex) AuthURL(state string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {y.ClientID}, "redirect_uri": {y.RedirectURI},
		"state": {state}, "force_confirm": {"no"}}
	return "https://oauth.yandex.ru/authorize?" + q.Encode()
}

func (y Yandex) Exchange(ctx context.Context, code string) (*Profile, error) {
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	err := postForm(ctx, "https://oauth.yandex.ru/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {y.ClientID}, "client_secret": {y.Secret},
	}, nil, &tok)
	if err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("yandex token: %v %s", err, tok.Error)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://login.yandex.ru/info?format=json", nil)
	req.Header.Set("Authorization", "OAuth "+tok.AccessToken)
	var info struct {
		ID           string `json:"id"`
		Login        string `json:"login"`
		DefaultEmail string `json:"default_email"`
		FirstName    string `json:"first_name"`
		LastName     string `json:"last_name"`
		DisplayName  string `json:"display_name"`
	}
	if err := doJSON(req, &info); err != nil {
		return nil, fmt.Errorf("yandex info: %w", err)
	}
	if info.ID == "" {
		return nil, ErrInvalid
	}
	display := info.DefaultEmail
	if display == "" {
		display = info.Login
	}
	return &Profile{Provider: "yandex", Subject: info.ID, Email: strings.ToLower(info.DefaultEmail),
		FirstName: info.FirstName, LastName: info.LastName, Display: display}, nil
}

// ---- VK ID (OAuth 2.1 with PKCE) ----

type VK struct {
	ClientID, Secret, RedirectURI string
}

func (v VK) AuthURL(state, verifier string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {v.ClientID}, "redirect_uri": {v.RedirectURI},
		"state": {state}, "code_challenge": {PKCEChallenge(verifier)}, "code_challenge_method": {"S256"},
		"scope": {"email"}}
	return "https://id.vk.com/authorize?" + q.Encode()
}

func (v VK) Exchange(ctx context.Context, code, deviceID, state, verifier string) (*Profile, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"client_id": {v.ClientID}, "device_id": {deviceID}, "redirect_uri": {v.RedirectURI}, "state": {state}}
	if v.Secret != "" {
		form.Set("client_secret", v.Secret)
	}
	var tok struct {
		AccessToken string      `json:"access_token"`
		UserID      json.Number `json:"user_id"`
		Error       string      `json:"error"`
		ErrorDesc   string      `json:"error_description"`
	}
	if err := postForm(ctx, "https://id.vk.com/oauth2/auth", form, nil, &tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("vk token: %v %s %s", err, tok.Error, tok.ErrorDesc)
	}
	var info struct {
		User struct {
			UserID    json.Number `json:"user_id"`
			FirstName string      `json:"first_name"`
			LastName  string      `json:"last_name"`
			Email     string      `json:"email"`
		} `json:"user"`
	}
	if err := postForm(ctx, "https://id.vk.com/oauth2/user_info", url.Values{
		"client_id": {v.ClientID}, "access_token": {tok.AccessToken},
	}, nil, &info); err != nil {
		return nil, fmt.Errorf("vk user_info: %w", err)
	}
	id := info.User.UserID.String()
	if id == "" || id == "0" {
		id = tok.UserID.String()
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return nil, ErrInvalid
	}
	display := strings.TrimSpace(info.User.FirstName + " " + info.User.LastName)
	if display == "" {
		display = "id" + id
	}
	return &Profile{Provider: "vk", Subject: id, Email: strings.ToLower(info.User.Email),
		FirstName: info.User.FirstName, LastName: info.User.LastName, Display: display}, nil
}
