// Package mailer sends HTML email over SMTP (STARTTLS on 587/25, implicit TLS on 465).
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"bshop/internal/settings"

	"golang.org/x/net/proxy"
)

type Mailer struct {
	get func() settings.SMTP

	mu   sync.Mutex
	last time.Time
}

func New(get func() settings.SMTP) *Mailer { return &Mailer{get: get} }

var ErrNotConfigured = errors.New("почта не настроена (SMTP)")

func (m *Mailer) Configured() bool { return m.get().Configured() }

// Send delivers one message, respecting the configured rate limit.
func (m *Mailer) Send(ctx context.Context, to, subject, html string) error {
	cfg := m.get()
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	m.throttle(cfg.RatePerSecond)
	c, err := session(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(cfg.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(build(cfg, to, subject, html)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Check connects, says hello, switches to TLS and logs in — everything Send does except sending (health check).
func (m *Mailer) Check(ctx context.Context) error {
	cfg := m.get()
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	c, err := session(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

// session opens an authenticated SMTP connection ready for MAIL FROM.
func session(ctx context.Context, cfg settings.SMTP) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := dial(ctx, cfg, addr)
	if err != nil {
		return nil, fmt.Errorf("smtp connect: %w", err)
	}
	if cfg.Port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: cfg.Host})
	}
	deadline := time.Now().Add(45 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp: %w", err)
	}
	helo := cfg.HELO
	if helo == "" {
		helo = "localhost"
	}
	fail := func(err error) (*smtp.Client, error) {
		c.Close()
		return nil, err
	}
	if err := c.Hello(helo); err != nil {
		return fail(err)
	}
	if cfg.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				return fail(fmt.Errorf("smtp starttls: %w", err))
			}
		}
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fail(fmt.Errorf("smtp auth: %w", err))
		}
	}
	return c, nil
}

func dial(ctx context.Context, cfg settings.SMTP, addr string) (net.Conn, error) {
	if cfg.ProxyURL == "" {
		d := net.Dialer{Timeout: 15 * time.Second}
		return d.DialContext(ctx, "tcp", addr)
	}
	u, err := url.Parse(cfg.ProxyURL)
	if err != nil {
		return nil, err
	}
	pd, err := proxy.FromURL(u, &net.Dialer{Timeout: 15 * time.Second})
	if err != nil {
		return nil, err
	}
	if cd, ok := pd.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, "tcp", addr)
	}
	return pd.Dial("tcp", addr)
}

func (m *Mailer) throttle(perSec int) {
	if perSec <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	gap := time.Second / time.Duration(perSec)
	if wait := time.Until(m.last.Add(gap)); wait > 0 {
		time.Sleep(wait)
	}
	m.last = time.Now()
}

func build(cfg settings.SMTP, to, subject, html string) []byte {
	from := cfg.From
	if cfg.FromName != "" {
		from = mime.QEncoding.Encode("utf-8", cfg.FromName) + " <" + cfg.From + ">"
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := cfg.From[strings.LastIndex(cfg.From, "@")+1:]
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%x@%s>\r\n", id, domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(html))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.Bytes()
}
