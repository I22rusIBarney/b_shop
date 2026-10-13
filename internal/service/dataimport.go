package service

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bshop/internal/db"
	"bshop/internal/importer"
)

// Import of an RWP database from the admin: the file is uploaded in small chunks (so no proxy body
// limit or request timeout gets in the way), restored into a scratch database on our Postgres server,
// checked with a dry run, and imported when the admin confirms. One import at a time.

const importDBName = "bshop_rwp_import"

// ImportState is what the admin page shows.
type ImportState struct {
	Stage     string           `json:"stage"` // idle | uploading | restoring | ready | importing | done | error
	FileName  string           `json:"file_name"`
	Size      int64            `json:"size"`
	Received  int64            `json:"received"`
	Log       []string         `json:"log"`
	Error     string           `json:"error"`
	Report    *importer.Report `json:"report"`
	Bots      []importer.Bot   `json:"bots"`
	BotID     int64            `json:"bot_id"`
	Existing  int64            `json:"existing"` // customers already in our database
	StartedAt *time.Time       `json:"started_at"`
	Synced    int              `json:"synced"`
}

type importJob struct {
	mu    sync.Mutex
	state ImportState
	path  string
}

func (j *importJob) logf(f string, a ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state.Log = append(j.state.Log, time.Now().Format("15:04:05")+"  "+fmt.Sprintf(f, a...))
}

func (j *importJob) set(fn func(*ImportState)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(&j.state)
}

func (s *Service) importJob() *importJob {
	s.importOnce.Do(func() { s.imp = &importJob{state: ImportState{Stage: "idle", Log: []string{}}} })
	return s.imp
}

// ImportStatus returns a copy of the current state.
func (s *Service) ImportStatus(ctx context.Context) ImportState {
	j := s.importJob()
	j.mu.Lock()
	st := j.state
	st.Log = append([]string{}, j.state.Log...)
	j.mu.Unlock()
	_ = s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&st.Existing)
	return st
}

// ImportChunk appends a chunk of the uploaded file. offset 0 starts a new upload.
func (s *Service) ImportChunk(name string, size, offset int64, body io.Reader) (ImportState, error) {
	j := s.importJob()
	j.mu.Lock()
	busy := j.state.Stage == "restoring" || j.state.Stage == "importing"
	j.mu.Unlock()
	if busy {
		return ImportState{}, userErr("идёт обработка предыдущего файла, дождитесь окончания")
	}
	if size <= 0 || size > 4<<30 {
		return ImportState{}, userErr("файл пустой или больше 4 ГБ")
	}
	if offset == 0 {
		s.dropImportDB(context.Background())
		if j.path != "" {
			_ = os.Remove(j.path)
		}
		dir := filepath.Join(os.TempDir(), "bshop-import")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return ImportState{}, err
		}
		j.path = filepath.Join(dir, "upload.bin")
		now := time.Now()
		j.set(func(st *ImportState) {
			*st = ImportState{Stage: "uploading", FileName: filepath.Base(name), Size: size, Log: []string{}, StartedAt: &now}
		})
	}
	j.mu.Lock()
	if j.state.Stage != "uploading" || j.state.Received != offset || j.state.Size != size {
		j.mu.Unlock()
		return ImportState{}, userErr("загрузка сбилась — начните заново")
	}
	j.mu.Unlock()
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return ImportState{}, err
	}
	n, err := io.Copy(f, io.LimitReader(body, 8<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Truncate(j.path, offset)
		return ImportState{}, err
	}
	j.set(func(st *ImportState) { st.Received += n })
	if offset+n >= size {
		j.set(func(st *ImportState) { st.Stage = "restoring" })
		j.logf("Файл получен: %s, %s", name, humanBytes(size))
		go s.restoreAndCheck(j)
	}
	return s.ImportStatus(context.Background()), nil
}

// importDSN is DATABASE_URL pointed at the scratch database.
func (s *Service) importDSN(dbname string) (string, error) {
	u, err := url.Parse(s.Cfg.DatabaseURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbname
	return u.String(), nil
}

func (s *Service) dropImportDB(ctx context.Context) {
	_, _ = s.Store.Pool.Exec(ctx, `DROP DATABASE IF EXISTS `+importDBName+` WITH (FORCE)`)
}

func (s *Service) restoreAndCheck(j *importJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	fail := func(err error) {
		j.logf("Ошибка: %v", err)
		j.set(func(st *ImportState) { st.Stage, st.Error = "error", err.Error() })
		s.dropImportDB(ctx)
	}
	file, kind, err := unpackDump(j.path, func(m string) { j.logf("%s", m) })
	if err != nil {
		fail(err)
		return
	}
	s.dropImportDB(ctx)
	if _, err := s.Store.Pool.Exec(ctx, `CREATE DATABASE `+importDBName); err != nil {
		fail(fmt.Errorf("не удалось создать временную базу: %w (у пользователя базы должно быть право CREATEDB)", err))
		return
	}
	dsn, err := s.importDSN(importDBName)
	if err != nil {
		fail(err)
		return
	}
	j.logf("Восстанавливаю базу (%s)…", map[string]string{"custom": "дамп pg_dump -Fc", "sql": "SQL-файл"}[kind])
	var cmd *exec.Cmd
	if kind == "custom" {
		cmd = exec.CommandContext(ctx, "pg_restore", "--no-owner", "--no-privileges", "--no-comments", "-d", dsn, file)
	} else {
		cmd = exec.CommandContext(ctx, "psql", "-q", "-X", "-v", "ON_ERROR_STOP=0", "-d", dsn, "-f", file)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	runErr := cmd.Run()
	if errors.Is(runErr, exec.ErrNotFound) {
		fail(errors.New("в образе нет pg_restore/psql — обновите контейнер"))
		return
	}
	if runErr != nil {
		// Restores routinely "fail" on roles, extensions and ownership that don't exist here; the data is what matters.
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		j.logf("Восстановление завершилось с предупреждениями (%d строк) — обычно это роли и права старого сервера", len(lines))
	}
	src, err := db.Open(ctx, dsn)
	if err != nil {
		fail(err)
		return
	}
	defer src.Close()
	if err := importer.LooksLikeRWP(ctx, src); err != nil {
		if runErr != nil {
			err = fmt.Errorf("%w. Ошибка восстановления: %s", err, firstLines(stderr.String(), 3))
		}
		fail(err)
		return
	}
	bots, _ := importer.ListBots(ctx, src)
	var botID int64
	if len(bots) > 1 {
		botID = bots[0].ID // the first bot is the owner's main bot; partner bots come later
	}
	j.set(func(st *ImportState) { st.Bots, st.BotID = bots, botID })
	j.logf("Пробный прогон…")
	rep, err := importer.Run(ctx, src, s.Store.Pool, importer.Options{DryRun: true, Wipe: true, BotID: botID})
	if err != nil {
		fail(err)
		return
	}
	_ = os.Remove(j.path)
	if file != j.path {
		_ = os.Remove(file)
	}
	j.logf("Готово к переносу: клиентов %d, подписок %d, платежей %d", rep.Counts["клиенты"], rep.Counts["подписки"], rep.Counts["платежи"])
	j.set(func(st *ImportState) { st.Stage, st.Report = "ready", rep })
}

// ImportRecheck re-runs the dry run for another bot of a multi-bot install.
func (s *Service) ImportRecheck(ctx context.Context, botID int64) (ImportState, error) {
	j := s.importJob()
	j.mu.Lock()
	ready := j.state.Stage == "ready"
	j.mu.Unlock()
	if !ready {
		return ImportState{}, userErr("сначала загрузите файл базы")
	}
	dsn, _ := s.importDSN(importDBName)
	src, err := db.Open(ctx, dsn)
	if err != nil {
		return ImportState{}, err
	}
	defer src.Close()
	rep, err := importer.Run(ctx, src, s.Store.Pool, importer.Options{DryRun: true, Wipe: true, BotID: botID})
	if err != nil {
		return ImportState{}, err
	}
	j.set(func(st *ImportState) { st.Report, st.BotID = rep, botID })
	return s.ImportStatus(ctx), nil
}

// ImportApply imports for real. wipe must be set when our database already has customers.
func (s *Service) ImportApply(ctx context.Context, wipe bool) (ImportState, error) {
	j := s.importJob()
	j.mu.Lock()
	if j.state.Stage != "ready" {
		j.mu.Unlock()
		return ImportState{}, userErr("сначала загрузите и проверьте файл базы")
	}
	botID := j.state.BotID
	j.state.Stage = "importing"
	j.mu.Unlock()
	var existing int64
	_ = s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&existing)
	if existing > 0 && !wipe {
		j.set(func(st *ImportState) { st.Stage = "ready" })
		return ImportState{}, userErr(fmt.Sprintf("в новой базе уже есть клиенты (%d). Отметьте «Заменить текущие данные», чтобы удалить их перед переносом", existing))
	}
	go s.applyImport(j, botID, wipe)
	return s.ImportStatus(ctx), nil
}

func (s *Service) applyImport(j *importJob, botID int64, wipe bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	fail := func(err error) {
		j.logf("Ошибка: %v", err)
		j.set(func(st *ImportState) { st.Stage, st.Error = "ready", err.Error() })
	}
	dsn, _ := s.importDSN(importDBName)
	src, err := db.Open(ctx, dsn)
	if err != nil {
		fail(err)
		return
	}
	if wipe {
		j.logf("Удаляю текущих клиентов, тарифы и промокоды…")
	}
	j.logf("Переношу данные…")
	keep := s.adminSessions(ctx)
	rep, err := importer.Run(ctx, src, s.Store.Pool, importer.Options{BotID: botID, Wipe: wipe, Force: wipe})
	src.Close()
	if err != nil {
		fail(err)
		return
	}
	if wipe {
		s.restoreAdminSessions(ctx, keep)
	}
	j.set(func(st *ImportState) { st.Report, st.Error = rep, "" })
	j.logf("Данные перенесены: клиентов %d, подписок %d, платежей %d", rep.Counts["клиенты"], rep.Counts["подписки"], rep.Counts["платежи"])
	s.dropImportDB(ctx)
	if s.Cfg.RemnawaveURL != "" {
		j.logf("Обновляю подписки из панели Remnawave…")
		n, problems, err := s.SyncFromPanel(ctx, func(done, total int) {
			if done > 0 && done < total {
				j.logf("  %d из %d", done, total)
			}
		})
		j.set(func(st *ImportState) { st.Synced = n })
		j.logf("Обновлено подписок: %d", n)
		for i, p := range problems {
			if i == 20 {
				j.logf("  …и ещё %d", len(problems)-20)
				break
			}
			j.logf("  %s", p)
		}
		if err != nil {
			j.logf("Ошибка обновления из панели: %v", err)
		}
	}
	j.logf("Готово")
	j.set(func(st *ImportState) { st.Stage = "done" })
}

// ImportCancel drops the scratch database and forgets the upload.
func (s *Service) ImportCancel(ctx context.Context) {
	j := s.importJob()
	j.mu.Lock()
	busy := j.state.Stage == "restoring" || j.state.Stage == "importing"
	j.mu.Unlock()
	if busy {
		return
	}
	s.dropImportDB(ctx)
	if j.path != "" {
		_ = os.Remove(j.path)
	}
	j.set(func(st *ImportState) { *st = ImportState{Stage: "idle", Log: []string{}} })
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}

// unpackDump finds the database dump in an upload: a pg_dump custom-format file, a plain SQL file,
// either of them gzipped, or a .tar/.tar.gz backup archive containing one. Returns the path and its kind.
func unpackDump(path string, log func(string)) (string, string, error) {
	for depth := 0; depth < 3; depth++ {
		head, err := readHead(path, 512)
		if err != nil {
			return "", "", err
		}
		switch {
		case bytes.HasPrefix(head, []byte("PGDMP")):
			return path, "custom", nil
		case len(head) > 2 && head[0] == 0x1f && head[1] == 0x8b:
			log("Распаковываю gzip…")
			out := path + ".ungz"
			if err := gunzip(path, out); err != nil {
				return "", "", fmt.Errorf("архив gzip повреждён: %w", err)
			}
			path = out
			continue
		case len(head) >= 262 && string(head[257:262]) == "ustar":
			log("Ищу дамп базы внутри архива…")
			out, err := extractDumpFromTar(path, log)
			if err != nil {
				return "", "", err
			}
			path = out
			continue
		case looksLikeSQL(head):
			return path, "sql", nil
		}
		return "", "", errors.New("не похоже на дамп PostgreSQL: нужен файл pg_dump (.dump/.sql), можно в .gz или .tar.gz")
	}
	return "", "", errors.New("слишком много вложенных архивов")
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	k, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:k], nil
}

func looksLikeSQL(head []byte) bool {
	s := strings.ToUpper(string(head))
	for _, m := range []string{"-- POSTGRESQL DATABASE DUMP", "SET STATEMENT_TIMEOUT", "CREATE TABLE", "\\RESTRICT", "SELECT PG_CATALOG", "-- DUMPED"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func gunzip(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(bufio.NewReader(in))
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, zr); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// extractDumpFromTar picks the largest file that looks like a database dump.
func extractDumpFromTar(path string, log func(string)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	tr := tar.NewReader(bufio.NewReader(f))
	out := path + ".member"
	var best int64 = -1
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("архив повреждён: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.ToLower(h.Name)
		if !(strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".dump") || strings.HasSuffix(name, ".sql.gz") ||
			strings.HasSuffix(name, ".dump.gz") || strings.HasSuffix(name, ".backup") || strings.Contains(name, "db")) {
			continue
		}
		if h.Size <= best {
			continue
		}
		w, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return "", err
		}
		w.Close()
		best = h.Size
		log("  нашёл " + h.Name)
	}
	if best < 0 {
		return "", errors.New("в архиве нет файла базы (.sql или .dump)")
	}
	return out, nil
}

// adminSession is an admin's login that must survive a wipe: the admin who pressed "Перенести"
// should stay signed in, attached to their account from the imported database.
type adminSession struct {
	tokenHash        []byte
	provider, ua, ip string
	expires          time.Time
	telegramID       *int64
	email            *string
	username, first  *string
}

func (s *Service) adminSessions(ctx context.Context) []adminSession {
	rows, err := s.Store.Pool.Query(ctx, `SELECT s.token_hash, s.provider, s.user_agent, s.ip, s.expires_at, c.telegram_id, c.email::text,
			c.username, c.first_name
		FROM sessions s JOIN customers c ON c.id = s.customer_id WHERE c.is_admin AND s.expires_at > now()`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []adminSession
	for rows.Next() {
		var a adminSession
		if rows.Scan(&a.tokenHash, &a.provider, &a.ua, &a.ip, &a.expires, &a.telegramID, &a.email, &a.username, &a.first) == nil {
			out = append(out, a)
		}
	}
	return out
}

func (s *Service) restoreAdminSessions(ctx context.Context, list []adminSession) {
	for _, a := range list {
		var id int64
		err := s.Store.Pool.QueryRow(ctx, `SELECT id FROM customers WHERE ($1::bigint IS NOT NULL AND telegram_id=$1) OR ($2::text IS NOT NULL AND email=$2)
			ORDER BY (telegram_id=$1) DESC NULLS LAST LIMIT 1`, a.telegramID, a.email).Scan(&id)
		if err != nil {
			// The admin wasn't a customer of the old bot: keep their account.
			if err := s.Store.Pool.QueryRow(ctx, `INSERT INTO customers (telegram_id, email, username, first_name, is_admin)
				VALUES ($1,$2,$3,$4,true) RETURNING id`, a.telegramID, a.email, a.username, a.first).Scan(&id); err != nil {
				s.Log.Warn("restore admin", "err", err)
				continue
			}
			if a.telegramID != nil {
				_, _ = s.Store.Pool.Exec(ctx, `INSERT INTO auth_identities (customer_id, provider, subject) VALUES ($1,'telegram',$2) ON CONFLICT DO NOTHING`,
					id, fmt.Sprint(*a.telegramID))
			}
		}
		_, _ = s.Store.Pool.Exec(ctx, `UPDATE customers SET is_admin=true WHERE id=$1`, id)
		_, _ = s.Store.Pool.Exec(ctx, `INSERT INTO sessions (token_hash, customer_id, provider, user_agent, ip, expires_at) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT DO NOTHING`, a.tokenHash, id, a.provider, a.ua, a.ip, a.expires)
	}
}
