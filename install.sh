#!/usr/bin/env bash
# Установка BobrGO Shop на чистый VPS (Ubuntu/Debian).
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/I22rusIBarney/b_shop/main/install.sh)
#
# Пока репозиторий закрытый — с токеном GitHub (classic, права repo и read:packages):
#   export GH_TOKEN=ghp_…
#   bash <(curl -fsSL -H "Authorization: token $GH_TOKEN" https://raw.githubusercontent.com/I22rusIBarney/b_shop/main/install.sh)
#
# Скрипт спрашивает домен, токен бота, ID админов и доступ к панели Remnawave, сам ставит Docker,
# генерирует пароли, настраивает HTTPS и запускает бота. Повторный запуск — обновление или смена настроек.
set -Eeuo pipefail

REPO="${BSHOP_REPO:-I22rusIBarney/b_shop}"
DIR="${BSHOP_DIR:-/opt/bshop}"
BRANCH="${BSHOP_BRANCH:-main}"
export GIT_TERMINAL_PROMPT=0 # закрытый репозиторий без токена — ошибка, а не зависший вопрос git

# ---------------------------------------------------------------- оформление
if [ -t 1 ]; then B=$'\e[1m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; C=$'\e[36m'; N=$'\e[0m'; else B='' G='' Y='' R='' C='' N=''; fi
say()  { printf '%s\n' "$*"; }
step() { printf '\n%s==> %s%s\n' "$B$C" "$*" "$N"; }
ok()   { printf '%s✓ %s%s\n' "$G" "$*" "$N"; }
warn() { printf '%s! %s%s\n' "$Y" "$*" "$N"; }
die()  { printf '%s✗ %s%s\n' "$R" "$*" "$N" >&2; exit 1; }
trap 'die "Ошибка в строке $LINENO. Вывод выше подскажет причину; скрипт можно запустить ещё раз."' ERR

# Вопросы читаем с терминала, даже когда скрипт пришёл через curl | bash.
exec 3</dev/tty || die "Нужен интерактивный терминал"
ask() { # ask VAR "Вопрос" [по умолчанию]
	local __v __def="${3:-}" __p="$2"
	[ -n "$__def" ] && __p="$__p ${C}[$__def]${N}"
	read -r -u 3 -p "$__p: " __v || true
	printf -v "$1" '%s' "${__v:-$__def}"
}
ask_secret() {
	local __v
	read -r -s -u 3 -p "$2: " __v || true
	echo
	printf -v "$1" '%s' "$__v"
}
yes_no() { # yes_no "Вопрос" [y|n]
	local __a __def="${2:-y}"
	read -r -u 3 -p "$1 $([ "$__def" = y ] && echo '[Д/н]' || echo '[д/Н]'): " __a || true
	__a="${__a:-$__def}"
	# case, а не [[ =~ ]]: в локали C на свежем сервере кириллица в [...] сравнивается побайтно.
	case "$__a" in y | Y | yes | Yes | д | Д | да | Да | ДА) return 0 ;; *) return 1 ;; esac
}

# ---------------------------------------------------------------- проверки
[ "$(id -u)" -eq 0 ] || die "Запустите от root (sudo -i)"
command -v apt-get >/dev/null || die "Поддерживаются Ubuntu и Debian"

say "${B}BobrGO Shop — установка${N}"
say "Папка: $DIR"

step "Пакеты"
export DEBIAN_FRONTEND=noninteractive
# На свежем сервере Ubuntu сама ставит обновления (unattended-upgrades) и держит блокировку apt — ждём, а не падаем.
APT=(apt-get -o DPkg::Lock::Timeout=600)
wait_apt() {
	local i=0
	while fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock >/dev/null 2>&1; do
		[ $i = 0 ] && say "Система сейчас ставит обновления — жду, пока apt освободится (до 10 минут)…"
		i=$((i + 1)); [ $i -gt 200 ] && die "apt занят больше 10 минут. Проверьте: ps aux | grep -E 'apt|dpkg'"
		sleep 3
	done
}
wait_apt
need=()
for p in curl git openssl ca-certificates cron; do dpkg -s "$p" >/dev/null 2>&1 || need+=("$p"); done
command -v ss >/dev/null || need+=(iproute2)
command -v dig >/dev/null || need+=(dnsutils)
if [ ${#need[@]} -gt 0 ]; then
	"${APT[@]}" update -qq && "${APT[@]}" install -y -qq "${need[@]}" >/dev/null
fi
ok "curl, git, openssl"

if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
	say "Ставлю Docker…"
	wait_apt
	curl -fsSL https://get.docker.com | sh >/dev/null 2>&1 || {
		warn "Официальный установщик Docker не сработал — ставлю Docker из репозитория дистрибутива"
		wait_apt
		"${APT[@]}" install -y -qq docker.io docker-compose-v2 >/dev/null || "${APT[@]}" install -y -qq docker.io docker-compose-plugin >/dev/null
	}
	systemctl enable --now docker >/dev/null 2>&1 || true
fi
docker compose version >/dev/null 2>&1 || die "Не удалось поставить Docker Compose"
ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null)"

# ---------------------------------------------------------------- исходники
step "Исходники"
if [ -d "$DIR/.git" ]; then
	git -C "$DIR" fetch -q origin "$BRANCH" && git -C "$DIR" reset -q --hard "origin/$BRANCH"
	ok "Обновлены до последней версии"
else
	mkdir -p "$(dirname "$DIR")"
	GH_TOKEN="${GH_TOKEN:-}"
	if [ -n "$GH_TOKEN" ] || ! git clone -q --depth 1 -b "$BRANCH" "https://github.com/$REPO.git" "$DIR" 2>/dev/null; then
		if [ -z "$GH_TOKEN" ]; then
			warn "Репозиторий $REPO закрытый — нужен токен GitHub с доступом на чтение."
			say "Создать: github.com → Settings → Developer settings → Personal access tokens → Tokens (classic),"
			say "галочки ${B}repo${N} и ${B}read:packages${N}."
			ask_secret GH_TOKEN "Токен GitHub"
		fi
		[ -n "$GH_TOKEN" ] || die "Без токена закрытый репозиторий не скачать"
		git clone -q --depth 1 -b "$BRANCH" "https://x-access-token:${GH_TOKEN}@github.com/$REPO.git" "$DIR" || die "Не удалось скачать репозиторий: проверьте токен"
		chmod 600 "$DIR/.git/config" # токен лежит в адресе репозитория — для обновлений
		echo "$GH_TOKEN" | docker login ghcr.io -u "${REPO%%/*}" --password-stdin >/dev/null 2>&1 || true
	fi
	ok "Скачаны в $DIR"
fi
cd "$DIR"

# ---------------------------------------------------------------- уже установлен?
# Значение из .env, а при прерванной установке — из черновика ответов .env.draft.
envget() {
	local f=.env
	[ -f .env ] || f=.env.draft
	grep -E "^$1=" "$f" 2>/dev/null | head -1 | cut -d= -f2- || true
}
# Ответы сохраняются сразу, чтобы при повторном запуске не вводить всё заново.
draft() {
	[ -f .env ] && return 0
	touch .env.draft && chmod 600 .env.draft
	grep -vE "^$1=" .env.draft >.env.draft.tmp 2>/dev/null || true
	printf '%s=%s\n' "$1" "$2" >>.env.draft.tmp
	mv .env.draft.tmp .env.draft
}
RECONFIGURE=1
if [ -f .env ]; then
	say
	say "BobrGO Shop уже установлен: $(envget PUBLIC_URL)"
	say "  1) Обновить и перезапустить (настройки не меняются)"
	say "  2) Пройти настройку заново (ключ шифрования и пароль базы сохранятся)"
	say "  0) Выйти"
	ask CH "Выбор" 1
	case "$CH" in
		1) RECONFIGURE=0 ;;
		2) RECONFIGURE=1 ;;
		*) exit 0 ;;
	esac
fi

[ ! -f .env ] && [ -f .env.draft ] && say "Продолжаю прерванную установку: прежние ответы подставлены — просто жмите Enter."
SERVER_IP="$(curl -4 -fsS --max-time 8 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')"

if [ "$RECONFIGURE" = 1 ]; then
	# ------------------------------------------------------------ вопросы
	step "Домен"
	say "Адрес, на котором откроется кабинет и Mini App, например ${B}shop.bobrgo.com${N}."
	say "В DNS у домена должна быть A-запись на этот сервер: ${B}$SERVER_IP${N}"
	while :; do
		ask DOMAIN "Домен" "$(envget DOMAIN)"
		DOMAIN="${DOMAIN#http://}"; DOMAIN="${DOMAIN#https://}"; DOMAIN="${DOMAIN%%/*}"
		[[ "$DOMAIN" =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || { warn "Это не похоже на домен"; continue; }
		RESOLVED="$(dig +short A "$DOMAIN" @1.1.1.1 2>/dev/null | tail -1)"
		if [ "$RESOLVED" = "$SERVER_IP" ]; then ok "$DOMAIN → $SERVER_IP"; break; fi
		warn "$DOMAIN указывает на «${RESOLVED:-никуда}», а этот сервер — $SERVER_IP."
		say "Если домен за Cloudflare с оранжевым облаком, выключите проксирование (серое облако) на время установки."
		yes_no "Продолжить всё равно?" n && break
	done
	draft DOMAIN "$DOMAIN"

	step "Telegram-бот"
	say "Токен из @BotFather. Если переезжаете со старого бота — тот же токен (старый бот перед запуском нового остановите)."
	while :; do
		ask_secret BOT_TOKEN "Токен бота$([ -n "$(envget BOT_TOKEN)" ] && echo ' (Enter — прежний)')"
		[ -z "$BOT_TOKEN" ] && BOT_TOKEN="$(envget BOT_TOKEN)"
		[[ "$BOT_TOKEN" =~ ^[0-9]+:[A-Za-z0-9_-]{30,}$ ]] || { warn "Токен выглядит как 123456:ABC…"; continue; }
		ME="$(curl -fsS --max-time 10 "https://api.telegram.org/bot$BOT_TOKEN/getMe" 2>/dev/null || true)"
		if [[ "$ME" == *'"ok":true'* ]]; then
			BOT_NAME="$(sed -n 's/.*"username":"\([^"]*\)".*/\1/p' <<<"$ME")"
			ok "Бот @$BOT_NAME"
			break
		fi
		if [ -z "$ME" ]; then warn "api.telegram.org не отвечает с этого сервера — проверить токен не получилось"; break; fi
		warn "Telegram не принял токен"
	done
	draft BOT_TOKEN "$BOT_TOKEN"

	say
	say "Telegram ID администраторов через запятую (свой ID можно узнать у @userinfobot)."
	while :; do
		ask ADMINS "ID админов" "$(envget ADMIN_TELEGRAM_IDS)"
		ADMINS="${ADMINS// /}"
		[[ "$ADMINS" =~ ^[0-9]+(,[0-9]+)*$ ]] && { draft ADMIN_TELEGRAM_IDS "$ADMINS"; break; }
		warn "Нужны числа через запятую, например 123456789"
	done

	step "Панель Remnawave"
	say "Ссылка, по которой открываете панель в браузере — можно целиком, вместе с секретным ключом"
	say "(вида ${B}https://panel.example.com/auth/login?AbCd=XyZ${N}): скрипт сам разберёт адрес и ключ."
	say "API-токен: панель → Настройки → API-токены → создать."
	RW_COOKIE="$(envget REMNAWAVE_COOKIE)"
	PANEL_OK=0
	while :; do
		ask RW_LINK "Ссылка на панель" "$(envget REMNAWAVE_URL)"
		RW_LINK="$(tr -d '[:space:]' <<<"$RW_LINK")"
		[[ "$RW_LINK" =~ ^https?:// ]] || RW_LINK="https://$RW_LINK"
		# Адрес — только схема и домен (порт), без пути и ключа.
		RW_URL="$(sed -E 's,^(https?://[^/?#]+).*,\1,' <<<"$RW_LINK")"
		# ?ключ=значение в ссылке — это защита reverse proxy (eGames и похожие): она же cookie для API.
		QS="${RW_LINK#*\?}"
		if [ "$QS" != "$RW_LINK" ] && [[ "$QS" =~ ^([A-Za-z0-9_-]+)=([A-Za-z0-9_-]+) ]]; then
			RW_COOKIE="${BASH_REMATCH[1]}=${BASH_REMATCH[2]}"
			ok "Адрес $RW_URL, ключ защиты ${BASH_REMATCH[1]}=…"
		fi
		draft REMNAWAVE_URL "$RW_URL"; draft REMNAWAVE_COOKIE "$RW_COOKIE"
		ask_secret RW_TOKEN "API-токен$([ -n "$(envget REMNAWAVE_TOKEN)" ] && echo ' (Enter — прежний)')"
		[ -z "$RW_TOKEN" ] && RW_TOKEN="$(envget REMNAWAVE_TOKEN)"
		check_panel() {
			local hdr=(-H "Authorization: Bearer $RW_TOKEN" -H "Accept: application/json")
			[ -n "$RW_COOKIE" ] && hdr+=(-H "Cookie: $RW_COOKIE")
			curl -s -o /tmp/bshop-rw.json -w '%{http_code}' --max-time 12 "${hdr[@]}" "$RW_URL/api/internal-squads" 2>/dev/null || true
		}
		CODE="$(check_panel)"
		if [ "$CODE" = 200 ] && grep -q '"response"' /tmp/bshop-rw.json 2>/dev/null; then ok "Панель отвечает, токен подходит"; PANEL_OK=1; break; fi
		case "$CODE" in
			401) warn "Панель отклонила токен (401). Нужен API-токен из настроек панели, не пароль." ;;
			000|"") warn "Панель не отвечает по адресу $RW_URL (сервер не достучался до неё)" ;;
			*)
				warn "Панель вернула $CODE. Похоже на защиту reverse proxy: вставьте ссылку целиком, с ?ключ=значение"
				ask RW_COOKIE "или введите ключ отдельно «имя=значение» (Enter — оставить)" "$RW_COOKIE"
				CODE="$(check_panel)"
				if [ "$CODE" = 200 ]; then ok "Панель отвечает"; PANEL_OK=1; break; fi
				[ "$CODE" = 401 ] && warn "Ключ подошёл, но панель отклонила токен (401)"
				;;
		esac
		yes_no "Ввести заново?" y || break
	done
	rm -f /tmp/bshop-rw.json
	draft REMNAWAVE_URL "$RW_URL"; draft REMNAWAVE_TOKEN "$RW_TOKEN"; draft REMNAWAVE_COOKIE "$RW_COOKIE"
	[ "$PANEL_OK" = 1 ] || warn "Продолжаю без проверки панели. Исправить адрес или токен позже: ${B}bshop env${N}"

	step "HTTPS"
	ask ACME_EMAIL "Почта для Let's Encrypt (Enter — без почты)" "$(envget ACME_EMAIL)"
	draft ACME_EMAIL "$ACME_EMAIL"

	# ------------------------------------------------------------ .env
	SECRET_KEY="$(envget SECRET_KEY)"; [ -n "$SECRET_KEY" ] || SECRET_KEY="$(openssl rand -hex 32)"
	PG_PASS="$(envget POSTGRES_PASSWORD)"; [ -n "$PG_PASS" ] || PG_PASS="$(openssl rand -hex 24)"
	OWNER="$(tr '[:upper:]' '[:lower:]' <<<"${REPO%%/*}")"

	# Порт приложения на 127.0.0.1: 8080, если свободен.
	PORT="$(envget BSHOP_PORT)"; PORT="${PORT:-8080}"
	port_busy() { ss -ltnH "sport = :$1" 2>/dev/null | grep -q .; }
	if [ ! -f .env ]; then while port_busy "$PORT"; do PORT=$((PORT + 1)); done; fi

	# Кто держит 80/443?
	PROXY=caddy
	if port_busy 80 || port_busy 443; then
		if systemctl is-active --quiet nginx 2>/dev/null; then PROXY=nginx; else PROXY=manual; fi
		[ "$(envget COMPOSE_PROFILES)" = caddy ] && PROXY=caddy # это наш Caddy
	fi

	umask 077
	cat >.env <<EOF
# BobrGO Shop — создан install.sh $(date +%F). Остальные настройки — в админке.
DOMAIN=$DOMAIN
PUBLIC_URL=https://$DOMAIN
BOT_TOKEN=$BOT_TOKEN
ADMIN_TELEGRAM_IDS=$ADMINS
REMNAWAVE_URL=$RW_URL
REMNAWAVE_TOKEN=$RW_TOKEN
REMNAWAVE_MODE=
REMNAWAVE_COOKIE=$RW_COOKIE

# Шифрует ключи в базе. НЕ МЕНЯЙТЕ и сохраните копию: без него не прочитать сохранённые ключи ЮKassa и др.
SECRET_KEY=$SECRET_KEY

POSTGRES_USER=bshop
POSTGRES_PASSWORD=$PG_PASS
POSTGRES_DB=bshop

BSHOP_IMAGE=ghcr.io/$OWNER/b_shop:latest
BSHOP_PORT=$PORT
ACME_EMAIL=$ACME_EMAIL
COMPOSE_PROFILES=$([ "$PROXY" = caddy ] && echo caddy)
EOF
	umask 022
	rm -f .env.draft
	ok "Настройки сохранены в $DIR/.env"
else
	DOMAIN="$(envget DOMAIN)"; PORT="$(envget BSHOP_PORT)"; ACME_EMAIL="$(envget ACME_EMAIL)"
	PROXY=manual
	[ "$(envget COMPOSE_PROFILES)" = caddy ] && PROXY=caddy
	[ -f /etc/nginx/sites-available/bshop.conf ] || [ -f /etc/nginx/conf.d/bshop.conf ] && PROXY=nginx
fi

# ---------------------------------------------------------------- HTTPS
step "HTTPS для $DOMAIN"
case "$PROXY" in
caddy)
	{
		if [ -n "$ACME_EMAIL" ]; then printf '{\n\temail %s\n}\n\n' "$ACME_EMAIL"; fi
		cat <<EOF
$DOMAIN {
	encode zstd gzip
	reverse_proxy shop:8080 {
		header_up X-Real-IP {remote_host}
	}
}
EOF
	} >Caddyfile
	ok "Порты 80/443 свободны — сертификат выпустит встроенный Caddy (отдельный контейнер, ничего на сервере не трогает)"
	;;
nginx)
	CONF=/etc/nginx/sites-available/bshop.conf
	[ -d /etc/nginx/sites-available ] || CONF=/etc/nginx/conf.d/bshop.conf
	if [ ! -f "$CONF" ] || [ "$RECONFIGURE" = 1 ]; then
		cat >"$CONF" <<EOF
# BobrGO Shop (создан install.sh). Отдельный файл — другие сайты nginx не трогаются.
server {
    listen 80;
    server_name $DOMAIN;
    client_max_body_size 25m;
    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 120s;
    }
}
EOF
		[ "$CONF" = /etc/nginx/sites-available/bshop.conf ] && ln -sf "$CONF" /etc/nginx/sites-enabled/bshop.conf
		nginx -t >/dev/null 2>&1 || die "nginx не принял конфиг $CONF — проверьте: nginx -t"
		systemctl reload nginx
		command -v certbot >/dev/null || { wait_apt; "${APT[@]}" install -y -qq certbot python3-certbot-nginx >/dev/null; }
		MAILARG=(--register-unsafely-without-email)
		[ -n "$ACME_EMAIL" ] && MAILARG=(-m "$ACME_EMAIL")
		if certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --redirect "${MAILARG[@]}" >/dev/null 2>&1; then
			ok "nginx: отдельный сайт $CONF, сертификат Let's Encrypt выпущен и будет продлеваться сам"
		else
			warn "Сертификат не выпустился (домен ещё не смотрит на сервер?). Повторить позже: certbot --nginx -d $DOMAIN"
		fi
	fi
	;;
manual)
	warn "Порты 80/443 заняты, и это не системный nginx (например, nginx панели Remnawave в Docker)."
	say "Бот будет доступен на ${B}127.0.0.1:$PORT${N}. Добавьте в свой reverse proxy сайт $DOMAIN:"
	cat <<EOF

    server {
        listen 443 ssl;
        server_name $DOMAIN;
        # ssl_certificate / ssl_certificate_key — сертификат для $DOMAIN
        client_max_body_size 25m;
        location / {
            proxy_pass http://127.0.0.1:$PORT;
            proxy_set_header Host \$host;
            proxy_set_header X-Real-IP \$remote_addr;
            proxy_set_header X-Forwarded-Proto https;
        }
    }

EOF
	say "Проще всего ставить бота на отдельный сервер — тогда скрипт всё настроит сам."
	;;
esac

# ---------------------------------------------------------------- запуск
step "Запуск"
chmod 600 .env
if docker compose pull -q shop 2>/dev/null; then
	ok "Образ скачан"
else
	warn "Готового образа нет — собираю на сервере (несколько минут, нужно ~2 ГБ памяти)"
	docker compose build shop
fi
docker compose up -d --remove-orphans

printf "Жду, пока бот поднимется"
for i in $(seq 1 60); do
	if curl -fsS --max-time 3 "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1; then echo; ok "Бот работает"; break; fi
	printf '.'
	sleep 2
	if [ "$i" = 60 ]; then echo; docker compose logs --tail 40 shop; die "Бот не запустился — выше последние строки лога"; fi
done

# ---------------------------------------------------------------- команда bshop и резервные копии
ln -sf "$DIR/scripts/bshop.sh" /usr/local/bin/bshop
chmod +x "$DIR/scripts/bshop.sh"
CRON="/etc/cron.d/bshop-backup"
echo "17 4 * * * root /usr/local/bin/bshop backup --quiet" >"$CRON"
ok "Команда bshop и ежедневная копия базы в 04:17 ($DIR/backups, хранятся 14 дней)"

if [ "$PROXY" != manual ]; then
	for i in $(seq 1 20); do
		curl -fsS --max-time 5 "https://$DOMAIN/api/health" >/dev/null 2>&1 && { ok "https://$DOMAIN открывается"; break; }
		sleep 3
		[ "$i" = 20 ] && warn "https://$DOMAIN пока не открывается — сертификат может выпускаться ещё минуту. Логи: bshop logs caddy"
	done
fi

# ---------------------------------------------------------------- итог
cat <<EOF

${B}${G}Готово!${N}

  Кабинет и админка:   ${B}https://$DOMAIN${N}  (войти через Telegram с аккаунта админа)
  Бот:                 ${BOT_NAME:+@$BOT_NAME}  — кнопка «Открыть» в боте ведёт в кабинет

${B}Что сделать дальше (в админке):${N}
  1. Настройки → Оплата и ЮKassa: Shop ID и секретный ключ.
     В личном кабинете ЮKassa → Интеграция → HTTP-уведомления:
     ${C}https://$DOMAIN/api/webhooks/yookassa${N}
  2. Настройки → Мой налог, Вход и почта — по желанию.
  3. Настройки → Вебхуки Remnawave → «Сгенерировать» и вписать строки в .env панели.
  4. Переезд со старого бота: Настройки → Перенос из RWP.
     Или подтянуть клиентов прямо из панели: Клиенты → «Синхронизировать с панелью».
  5. Вход через Telegram в обычном браузере: @BotFather → /setdomain → ${BOT_NAME:+@$BOT_NAME →} $DOMAIN

${B}Управление:${N}  bshop status | logs | restart | update | backup | restore | env

  Сохраните себе SECRET_KEY из $DIR/.env — без него не расшифровать ключи в базе.
EOF
