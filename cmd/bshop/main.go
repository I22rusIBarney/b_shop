package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bshop/internal/api"
	"bshop/internal/bot"
	"bshop/internal/config"
	"bshop/internal/db"
	"bshop/internal/mailer"
	"bshop/internal/remnawave"
	"bshop/internal/secrets"
	"bshop/internal/service"
	"bshop/internal/settings"
	"bshop/internal/store"
	"bshop/web"
)

var Version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	box, err := secrets.New(cfg.SecretKey)
	if err != nil {
		return err
	}
	st := settings.NewStore(pool, box)
	if err := st.Load(ctx); err != nil {
		return err
	}

	svc := &service.Service{
		Cfg:      cfg,
		Store:    store.New(pool),
		Settings: st,
		Box:      box,
		Panel:    remnawave.New(cfg.RemnawaveURL, cfg.RemnawaveToken, remnawave.Options{Local: cfg.RemnawaveLocal, Cookie: cfg.RemnawaveCookie}),
		Mailer:   mailer.New(func() settings.SMTP { return st.Get().SMTP }),
		Notify:   bot.LogNotifier{Log: log},
		Log:      log,
	}

	api.Version = Version
	srv := api.New(svc, web.FS(), log)

	if cfg.BotToken != "" {
		b, err := bot.New(cfg.BotToken, svc, log)
		if err != nil {
			return err
		}
		svc.Notify = b
		api.BotUsername = b.Username
		svc.BotUsername = b.Username
		go b.Run(ctx)
		log.Info("telegram bot started", "username", b.Username)
	}

	svc.RunWorkers(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", cfg.HTTPAddr, "version", Version)
		errc <- httpSrv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdown)
}
