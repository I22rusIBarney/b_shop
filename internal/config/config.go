// Package config reads the few bootstrap values that must come from the environment.
// Everything a shop owner may want to change lives in the database (package settings).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL    string
	HTTPAddr       string
	PublicURL      string // https://shop.example.com, no trailing slash
	BotToken       string
	SecretKey      string // 32+ chars; encrypts stored secrets and signs tokens
	AdminTelegram  []int64
	RemnawaveURL   string
	RemnawaveToken string
	// RemnawaveLocal adds X-Forwarded-* headers needed when the panel is reached over plain HTTP inside Docker.
	RemnawaveLocal bool
	// RemnawaveCookie is an optional "name=value" cookie some panels require behind a reverse proxy.
	RemnawaveCookie string
	Dev             bool
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		HTTPAddr:        getenv("HTTP_ADDR", ":8080"),
		PublicURL:       strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		BotToken:        os.Getenv("BOT_TOKEN"),
		SecretKey:       os.Getenv("SECRET_KEY"),
		RemnawaveURL:    strings.TrimRight(os.Getenv("REMNAWAVE_URL"), "/"),
		RemnawaveToken:  os.Getenv("REMNAWAVE_TOKEN"),
		RemnawaveLocal:  os.Getenv("REMNAWAVE_MODE") == "local",
		RemnawaveCookie: os.Getenv("REMNAWAVE_COOKIE"),
		Dev:             os.Getenv("DEV") == "true",
	}
	for _, part := range strings.FieldsFunc(os.Getenv("ADMIN_TELEGRAM_IDS"), func(r rune) bool { return r == ',' || r == ' ' }) {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ADMIN_TELEGRAM_IDS: bad id %q", part)
		}
		c.AdminTelegram = append(c.AdminTelegram, id)
	}

	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.PublicURL == "" {
		errs = append(errs, errors.New("PUBLIC_URL is required"))
	}
	if len(c.SecretKey) < 32 {
		errs = append(errs, errors.New("SECRET_KEY must be at least 32 characters"))
	}
	if !c.Dev {
		if c.BotToken == "" {
			errs = append(errs, errors.New("BOT_TOKEN is required"))
		}
		if c.RemnawaveURL == "" || c.RemnawaveToken == "" {
			errs = append(errs, errors.New("REMNAWAVE_URL and REMNAWAVE_TOKEN are required"))
		}
	}
	return c, errors.Join(errs...)
}

func (c *Config) IsAdminTelegram(id int64) bool {
	for _, a := range c.AdminTelegram {
		if a == id {
			return true
		}
	}
	return false
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
