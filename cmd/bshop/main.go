package main

import (
	"bshop/internal/backup"
	"context"
	"errors"
	"flag"
	"fmt"
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
	"bshop/internal/importer"
	"bshop/internal/mailer"
	"bshop/internal/remnawave"
	"bshop/internal/secrets"
	"bshop/internal/service"
	"bshop/internal/settings"
	"bshop/internal/store"
	"bshop/web"

	"github.com/jackc/pgx/v5/pgxpool"
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
	// Works without a database or settings: used to restore on a fresh server.
	if len(os.Args) > 1 && os.Args[1] == "decrypt-backup" {
		return backup.DecryptCommand(os.Stdin, os.Stdout)
	}
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

	if len(os.Args) > 1 && os.Args[1] == "import-rwp" {
		return importRWP(ctx, svc, pool, os.Args[2:])
	}

	api.Version = Version
	srv := api.New(svc, web.FS(), log)

	if cfg.BotToken != "" {
		b, err := bot.New(cfg.BotToken, svc, log)
		if err != nil {
			return err
		}
		svc.Notify = b
		svc.Channels = b
		svc.Files = b
		svc.Photos = b
		svc.Telegram = b
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

// importRWP: bshop import-rwp --from postgres://… [--dry-run] [--bot N] [--force] [--no-sync]
func importRWP(ctx context.Context, svc *service.Service, dst *pgxpool.Pool, args []string) error {
	fs := flag.NewFlagSet("import-rwp", flag.ContinueOnError)
	from := fs.String("from", "", "адрес базы RWP, postgres://user:pass@host:5432/db")
	dry := fs.Bool("dry-run", false, "только показать, что будет перенесено (ничего не записывается)")
	bot := fs.Int64("bot", 0, "перенести только один бот RWP (id из таблицы bot), 0 — все")
	force := fs.Bool("force", false, "разрешить импорт в базу, где уже есть клиенты")
	noSync := fs.Bool("no-sync", false, "не обновлять подписки из панели Remnawave после импорта")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return errors.New("укажите --from postgres://…")
	}
	src, err := db.Open(ctx, *from)
	if err != nil {
		return fmt.Errorf("база RWP: %w", err)
	}
	defer src.Close()
	rep, err := importer.Run(ctx, src, dst, importer.Options{DryRun: *dry, BotID: *bot, Force: *force})
	if rep != nil {
		fmt.Print(rep.String())
	}
	if err != nil {
		return err
	}
	if *dry {
		fmt.Println("\nПробный прогон: ничего не записано. Уберите --dry-run, чтобы перенести.")
		return nil
	}
	fmt.Println("\nДанные перенесены.")
	if *noSync || svc.Cfg.RemnawaveURL == "" {
		return nil
	}
	fmt.Println("Обновляю подписки из панели Remnawave…")
	n, problems, err := svc.SyncFromPanel(ctx, func(done, total int) { fmt.Printf("  %d / %d\n", done, total) })
	fmt.Printf("Обновлено подписок: %d\n", n)
	for _, p := range problems {
		fmt.Println("  - " + p)
	}
	return err
}
