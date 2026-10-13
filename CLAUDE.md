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
- `internal/store` — SQL; `internal/api` — HTTP (routes.go кабинет, admin.go, stage3.go); `internal/bot` — Telegram.
- `web/src/pages` — кабинет (Home, Buy, ChangePlan, Referral, Profile, Install…), `web/src/pages/admin` — админка.
- `docs/SPEC.md` — полный список функций по скриншотам RWP; `import/automations-*.json` — 10 сценариев автоматизаций.

## Проверка
- Go: `GOTOOLCHAIN=local GOFLAGS=-mod=mod GOPROXY=off go vet ./...`
- Тесты (нужен Postgres): `TEST_DATABASE_URL=postgres://bshop:bshop@127.0.0.1:5432/bshop_test?sslmode=disable go test ./internal/service`
  (если Postgres лежит: `pg_ctlcluster 16 main start`).
- Фронт: `cd web && npx tsc --noEmit -p . && npx vite build`.

## Статус (13.10.2026)
- Этапы 1–3 готовы: каталог, покупка/продление, чеки, триал, кабинет в стиле BobrGO, устройства (с переименованием),
  смена тарифа (доплата/конвертация/понижение), промокоды (скидка %/дни), реферальные бонусы с уровнями, доп. устройства,
  автопродление, админка (клиенты, платежи, тарифы, промокоды, настройки, оформление).
- Не проверено на реальных ЮKassa/Remnawave — ждём тестовый стенд Алексея.

## Дальше
- Этап 4: тикеты + ИИ-поддержка (OpenRouter-совместимый, его Gemini-прокси), автоматизации (импорт JSON),
  рассылки → лента «Новости», уведомления админам по событиям в разные чаты/топики с шаблонами и кнопкой «Открыть клиента».
- Этап 5: статистика, импорт из БД RWP, перенос (новый секрет вебхука Remnawave, перенос VAPID-ключей).

## Как работать экономно
- Один этап — одна новая сессия; контекст брать из этого файла и SPEC.md, а не перечитывать весь код.
- Читать файлы точечно (grep / offset), скриншоты — только итоговые, коммитить по завершении этапа.
