# BobrGO Shop — заметки для продолжения работы

Свой бот + веб-кабинет для VPN BobrGO (панель Remnawave 3.x) вместо платного RWP Shop.
Основа — идеи open-source Jolymmiels/remnawave-telegram-shop (AGPL-3.0: при запуске опубликовать исходники).
Код RWP не используем и не декомпилируем. Секреты — только в .env на сервере или зашифрованы в БД, никогда в чат.

## Стек
- Go 1.24 (модуль `bshop`), pgx/v5, go-telegram/bot, net/http ServeMux; миграции `internal/db/migrations` (embed).
- React 19 + TS + Vite в `web/`, собирается в `web/dist` и встраивается в бинарник.
- Оплата только ЮKassa (СБП), чеки «Мой налог», вход: Telegram / email-код / Яндекс / VK.
- Всё настраивается из админки (settings-секции JSON в таблице `settings`, `internal/settings/sections.go`).

## Карта кода
- `internal/service` — бизнес-логика: purchase.go (покупка/продление/ApplyPayment), change.go (смена тарифа, доп. устройства),
  promo.go, referral.go, autopay.go, workers.go (фоновые задачи), provision.go (Remnawave), subscriptions.go, auth.go.
  Этап 4: notify.go (каталог админ-событий, шаблоны, маршруты чат/топик), support.go (тикеты, ИИ первой линии, таймеры),
  broadcasts.go (рассылки → «Новости»), automations.go (движок сценариев, импорт/экспорт RWP, предупреждения).
- `internal/ai` — клиент OpenAI-совместимого API. `internal/store` — SQL; `internal/api` — HTTP (routes.go кабинет, admin.go,
  stage3.go, stage4.go); `internal/bot` — Telegram (Deliver: TG/почта; Admin: чат/топик; сообщения боту → тикеты).
- `web/src/pages` — кабинет (Home, Buy, ChangePlan, Referral, Profile, Install…), `web/src/pages/admin` — админка.
- `docs/SPEC.md` — полный список функций по скриншотам RWP; `import/automations-*.json` — 10 сценариев автоматизаций.

## Проверка
- Go: `GOTOOLCHAIN=local GOFLAGS=-mod=mod GOPROXY=off go vet ./...`
  Если модулей нет в кеше: proxy.golang.org закрыт, GitHub открыт → `GOPROXY=direct GOSUMDB=off`, а golang.org/x/{net,crypto,sync,text}
  клонировать с github.com/golang/* и подключить replace через отдельный `-modfile` (не коммитить). Postgres-роль bshop/bshop
  и БД bshop_test в свежей среде создать вручную.
- Тесты (нужен Postgres): `TEST_DATABASE_URL=postgres://bshop:bshop@127.0.0.1:5432/bshop_test?sslmode=disable go test ./internal/service`
  (если Postgres лежит: `pg_ctlcluster 16 main start`).
- Фронт: `cd web && npx tsc --noEmit -p . && npx vite build`.

## Статус (13.10.2026)
- Этапы 1–3 готовы: каталог, покупка/продление, чеки, триал, кабинет в стиле BobrGO, устройства (с переименованием),
  смена тарифа (доплата/конвертация/понижение), промокоды (скидка %/дни), реферальные бонусы с уровнями, доп. устройства,
  автопродление, админка (клиенты, платежи, тарифы, промокоды, настройки, оформление).
- Не проверено на реальных ЮKassa/Remnawave — ждём тестовый стенд Алексея.

- Этап 4 готов (13.10.2026): обращения (кабинет + чат в боте, вложения, оценка, автозакрытие), ИИ-ассистент
  (порог уверенности → черновик, эскалация, база знаний + FAQ, подсказка оператору), шаблоны «/», рассылки с сегментами,
  тестом, отложенной отправкой и лентой «Новости», уведомления админам (вкл/чат/топик/шаблон/тест), автоматизации
  (10 сценариев RWP импортируются как есть, скоринг лидов, тихие часы, {promo:…}, конверсия, участие в карточке клиента),
  deep links ?startapp=plans|referrals|support|news|ticket_N. Автоматизации по умолчанию выключены (Настройки → Триггеры).
- RAG без pgvector: статьи подбираются по совпадению слов в пределах лимита символов. Предложения статей из тикетов,
  автоответы по расписанию, уведомления о нодах/abuse/torrent — не сделаны (нужны вебхуки Remnawave, этап 5).

- Этап 5 готов (13.10.2026): статистика (stats.go: обзор, юнит-экономика/MRR по покрытию конца месяца, платежи,
  рефералы, поддержка, аналитика: воронка, лиды со скором и рассылкой на сегмент lead_*), графики — web/src/ui/charts.tsx
  (свои SVG, палитра проверена для тёмной темы). Вебхуки Remnawave (webhooks.go, POST /api/webhooks/remnawave, HMAC
  X-Remnawave-Signature, журнал webhook_events, дедуп по хешу тела): сообщения клиентам по событиям (шаблоны в
  settings.webhooks), события нод/входов/торрентов/биллинга → админ-события группы Remnawave. Импорт из RWP:
  internal/importer (команда `bshop import-rwp --from DSN [--dry-run]`, одна транзакция, потом SyncFromPanel),
  тест на схеме RWP из testdata; инструкция переезда — docs/MIGRATION.md. Web Push не делали (VAPID не нужен).

- Промокоды kind=unlock открывают скрытые тарифы (plan_ids → таблица unlocked_plans; planHidden в purchase.go).
  Импорт из админки: Настройки → Перенос из RWP (service/dataimport.go: загрузка частями по 900 КБ, распаковка
  .dump/.sql/.gz/.tar, pg_restore/psql во временную БД bshop_rwp_import, пробный прогон с Wipe, применение,
  сессии админа переносятся). В образе нужен postgresql17-client (Dockerfile, alpine 3.21).

- Установка: install.sh (вопросы с проверкой домена/токена/панели, Docker, .env, HTTPS: свой Caddy-профиль если 80/443
  свободны / отдельный сайт в системном nginx + certbot / ручной блок для чужого прокси), команда bshop (scripts/bshop.sh:
  update/backup/restore/logs…), cron-бэкап. Образ собирает GitHub Actions (.github/workflows/image.yml) → ghcr.io/i22rusibarney/b_shop.
  Кнопка меню бота ставится сама (SetChatMenuButton в bot.Run).

- 14.10.2026: главная на один экран (.app:has(> .home) — flex по --tg-viewport-stable-height), установка: обзор с
  автоопределением устройства → шаги (Install.tsx нормализует конфиг Remnawave и ручной список в GPlatform/GStep),
  ссылки happ:// и т.п. из Telegram идут через GET /open?to=… (internal/api/openapp.go).

## Дальше
- Боевой переезд по docs/MIGRATION.md на тестовом стенде Алексея; проверить на реальных ЮKassa/Remnawave.
- Не сделано: тексты бота из translations RWP, предложения статей БЗ из тикетов, abuse-детектор (много IP).

## Как работать экономно
- Один этап — одна новая сессия; контекст брать из этого файла и SPEC.md, а не перечитывать весь код.
- Читать файлы точечно (grep / offset), скриншоты — только итоговые, коммитить по завершении этапа.
