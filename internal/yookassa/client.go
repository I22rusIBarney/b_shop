// Package yookassa implements the parts of the YooKassa API v3 the shop needs.
package yookassa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base   string
	shopID string
	secret string
	http   *http.Client
}

func New(base, shopID, secret string) *Client {
	if base == "" {
		base = "https://api.yookassa.ru/v3"
	}
	return &Client{base: strings.TrimRight(base, "/"), shopID: shopID, secret: secret,
		http: &http.Client{Timeout: 30 * time.Second}}
}

type Amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

// Kopecks converts an integer amount in kopecks to a YooKassa amount.
func Kopecks(k int64) Amount {
	return Amount{Value: fmt.Sprintf("%d.%02d", k/100, k%100), Currency: "RUB"}
}

// ToKopecks parses "123.45" into 12345.
func (a Amount) ToKopecks() int64 {
	var rub, kop int64
	parts := strings.SplitN(a.Value, ".", 2)
	fmt.Sscan(parts[0], &rub)
	if len(parts) == 2 {
		frac := (parts[1] + "00")[:2]
		fmt.Sscan(frac, &kop)
	}
	return rub*100 + kop
}

type Confirmation struct {
	Type            string `json:"type"`
	ReturnURL       string `json:"return_url,omitempty"`
	ConfirmationURL string `json:"confirmation_url,omitempty"`
}

type MethodData struct {
	Type string `json:"type"`
}

type PaymentMethod struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Saved bool   `json:"saved"`
	Title string `json:"title"`
	Card  *struct {
		Last4    string `json:"last4"`
		CardType string `json:"card_type"`
	} `json:"card,omitempty"`
}

type CreatePayment struct {
	Amount            Amount            `json:"amount"`
	Capture           bool              `json:"capture"`
	Confirmation      *Confirmation     `json:"confirmation,omitempty"`
	Description       string            `json:"description,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	PaymentMethodData *MethodData       `json:"payment_method_data,omitempty"`
	PaymentMethodID   string            `json:"payment_method_id,omitempty"`
	SavePaymentMethod bool              `json:"save_payment_method,omitempty"`
	MerchantCustomer  string            `json:"merchant_customer_id,omitempty"`
}

type Payment struct {
	ID                  string            `json:"id"`
	Status              string            `json:"status"` // pending, waiting_for_capture, succeeded, canceled
	Paid                bool              `json:"paid"`
	Amount              Amount            `json:"amount"`
	Confirmation        *Confirmation     `json:"confirmation"`
	Metadata            map[string]string `json:"metadata"`
	PaymentMethod       *PaymentMethod    `json:"payment_method"`
	CreatedAt           time.Time         `json:"created_at"`
	CapturedAt          *time.Time        `json:"captured_at"`
	CancellationDetails *struct {
		Party  string `json:"party"`
		Reason string `json:"reason"`
	} `json:"cancellation_details"`
}

type Refund struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	Amount    Amount `json:"amount"`
}

type Error struct {
	Status      int
	Code        string `json:"code"`
	Description string `json:"description"`
	Parameter   string `json:"parameter"`
}

func (e *Error) Error() string {
	s := fmt.Sprintf("yookassa %d %s: %s", e.Status, e.Code, e.Description)
	if e.Parameter != "" {
		s += " (" + e.Parameter + ")"
	}
	return s
}

func (c *Client) do(ctx context.Context, method, path, idemKey string, body, out any) error {
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
	req.SetBasicAuth(c.shopID, c.secret)
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotence-Key", idemKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("yookassa: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		e := &Error{Status: resp.StatusCode}
		_ = json.Unmarshal(data, e)
		if e.Description == "" {
			e.Description = strings.TrimSpace(string(data))
		}
		return e
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// CreatePayment creates a payment; idemKey makes retries safe (use the local payment id).
func (c *Client) CreatePayment(ctx context.Context, idemKey string, in CreatePayment) (*Payment, error) {
	var p Payment
	if err := c.do(ctx, http.MethodPost, "/payments", idemKey, in, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) GetPayment(ctx context.Context, id string) (*Payment, error) {
	var p Payment
	if err := c.do(ctx, http.MethodGet, "/payments/"+id, "", nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) GetRefund(ctx context.Context, id string) (*Refund, error) {
	var r Refund
	if err := c.do(ctx, http.MethodGet, "/refunds/"+id, "", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) CreateRefund(ctx context.Context, idemKey, paymentID string, amount Amount) (*Refund, error) {
	var r Refund
	if err := c.do(ctx, http.MethodPost, "/refunds", idemKey, map[string]any{"payment_id": paymentID, "amount": amount}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Ping checks credentials by reading the shop info.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/me", "", nil, nil)
}

// Notification is the webhook body. The object is re-fetched from the API before trusting it.
type Notification struct {
	Type   string `json:"type"`
	Event  string `json:"event"`
	Object struct {
		ID        string `json:"id"`
		PaymentID string `json:"payment_id"`
	} `json:"object"`
}
