#!/usr/bin/env bash
# Управление BobrGO Shop: bshop <команда>. Ставится install.sh как /usr/local/bin/bshop.
set -Eeuo pipefail
DIR="$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)"
cd "$DIR"
B=$'\e[1m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; N=$'\e[0m'
[ -t 1 ] || { B='' G='' Y='' R='' N=''; }
ok() { printf '%s✓ %s%s\n' "$G" "$*" "$N"; }
die() { printf '%s✗ %s%s\n' "$R" "$*" "$N" >&2; exit 1; }
envget() { grep -E "^$1=" .env 2>/dev/null | head -1 | cut -d= -f2- || true; }
dc() { docker compose "$@"; }
BACKUPS="$DIR/backups"
PORT="$(envget BSHOP_PORT)"; PORT="${PORT:-8080}"

health() { curl -fsS --max-time 3 "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1; }
wait_up() {
	for _ in $(seq 1 60); do health && { ok "Бот работает: $(envget PUBLIC_URL)"; return; }; sleep 2; done
	dc logs --tail 40 shop
	die "Бот не поднялся — выше последние строки лога"
}

cmd="${1:-help}"
shift || true
case "$cmd" in
status)
	dc ps
	health && ok "Отвечает на 127.0.0.1:$PORT" || printf '%s! Не отвечает на 127.0.0.1:%s%s\n' "$Y" "$PORT" "$N"
	;;
logs)
	dc logs -f --tail "${LINES:-200}" "${1:-shop}"
	;;
restart)
	dc up -d --remove-orphans && dc restart shop && wait_up
	;;
stop)
	dc stop
	;;
update)
	"$0" backup --quiet || true
	if [ -d .git ]; then git fetch -q origin && git reset -q --hard "origin/$(git rev-parse --abbrev-ref HEAD)"; ok "Код обновлён: $(git log -1 --format='%h %s')"; fi
	# Только готовый образ (≈15 МБ). Сборка на сервере качает ~2 ГБ и идёт минуты — лишь по явной просьбе: BSHOP_BUILD=1 bshop update
	if [ "${BSHOP_BUILD:-}" = 1 ]; then dc build shop
	elif err=$(dc pull -q shop 2>&1); then ok "Новый образ скачан"
	else printf '%s✗ Не удалось скачать образ:%s %s\nПроверьте интернет и что пакет на GitHub публичный. Собрать на сервере: BSHOP_BUILD=1 bshop update\n' "$R" "$N" "$err"; exit 1; fi
	dc up -d --remove-orphans
	wait_up
	docker image prune -f >/dev/null 2>&1 || true
	;;
backup)
	mkdir -p "$BACKUPS"
	f="$BACKUPS/bshop-$(date +%F-%H%M).dump"
	dc exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' >"$f"
	cp .env "$BACKUPS/env-$(date +%F).bak" && chmod 600 "$BACKUPS"/env-*.bak
	find "$BACKUPS" -name 'bshop-*.dump' -mtime +14 -delete
	find "$BACKUPS" -name 'env-*.bak' -mtime +14 -delete
	[ "${1:-}" = --quiet ] || ok "Копия базы: $f ($(du -h "$f" | cut -f1))"
	;;
restore)
	f="${1:-}"
	[ -f "$f" ] || die "Укажите файл: bshop restore $BACKUPS/bshop-….dump"
	read -r -p "Заменить текущую базу содержимым $f? Напишите ДА: " a
	[ "$a" = ДА ] || exit 1
	"$0" backup --quiet
	dc stop shop
	dc exec -T db sh -c 'dropdb -U "$POSTGRES_USER" --if-exists "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" "$POSTGRES_DB"'
	dc exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner' <"$f" || true
	dc start shop && wait_up
	;;
env)
	"${EDITOR:-nano}" .env
	dc up -d && dc restart shop && wait_up
	;;
uninstall)
	read -r -p "Удалить BobrGO Shop вместе с базой? Сначала будет сделана копия базы. Напишите УДАЛИТЬ: " a
	[ "$a" = УДАЛИТЬ ] || exit 1
	"$0" backup
	dc down -v
	rm -f /usr/local/bin/bshop /etc/cron.d/bshop-backup /etc/nginx/sites-enabled/bshop.conf
	ok "Удалено. Копии базы и .env остались в $BACKUPS"
	;;
*)
	cat <<HELP
${B}bshop${N} — управление BobrGO Shop ($DIR)

  bshop status          состояние контейнеров
  bshop logs [сервис]   журнал (shop, db, caddy), Ctrl+C — выход
  bshop restart         перезапуск
  bshop update          копия базы → новая версия → перезапуск
  bshop backup          копия базы сейчас (каждую ночь делается сама)
  bshop restore ФАЙЛ    вернуть базу из копии
  bshop env             поправить .env и перезапустить
  bshop stop            остановить
  bshop uninstall       удалить (с копией базы)
HELP
	;;
esac
