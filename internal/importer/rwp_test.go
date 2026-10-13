package importer_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"bshop/internal/db"
	"bshop/internal/importer"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Sample RWP data in the shapes seen in a real install: rubles as numeric, "months" units,
// "purchase" for both first buys and renewals, "paid"/"cancel" statuses, uuid customer ids.
const sample = `
INSERT INTO bot (id, type, tg_username) VALUES (1, 'main', 'bobrevpn_bot');
INSERT INTO plan (id, name, type, traffic_limit, device_limit, is_active, is_default, internal_squads, remnawave_tag, sort_order, traffic_limit_strategy, allow_extra_devices, max_total_devices)
VALUES (1, '🟤 Бобрёнок', 'subscription', 0, 3, true, true, '["11111111-1111-1111-1111-111111111111"]', 'BOBR', 1, 'MONTH', true, 6),
       (2, '🟣 Бобр Мастер', 'subscription', 75, 3, true, false, '', '', 2, 'NO_RESET', false, 0),
       (8, 'Трафик', 'traffic', 0, 1, true, false, '', '', 9, 'MONTH', false, 0);
INSERT INTO plan_prices (plan_id, price, currency, period_value, period_unit, is_active, price_type, is_highlighted) VALUES
 (1, 299.00, 'RUB', 1, 'months', true, 'base', false), (1, 840.00, 'RUB', 3, 'months', true, 'base', true),
 (1, 50.00, 'RUB', 1, 'months', true, 'extra_device', false),
 (2, 180.00, 'RUB', 1, 'months', true, 'base', false), (8, 60.00, 'RUB', NULL, NULL, true, NULL, false);
INSERT INTO customer (id, telegram_id, email, tg_username, tg_first_name, language, is_blocked, is_blocked_by_user, trial_used, created_at,
  referral_code, lead_temperature, yandex_id, discount_percent, admin_tag, bot_id) VALUES
 ('aaaaaaaa-0000-0000-0000-000000000001', 1001, 'A@Mail.ru', 'alex', 'Алексей', 'ru', false, false, true, now() - interval '200 days', 'ALEX1', 'stable', 'y-1', 10, 'VIP', 1),
 ('aaaaaaaa-0000-0000-0000-000000000002', 1002, NULL, 'bob', 'Боб', 'ru', false, true, true, now() - interval '100 days', 'BOB22', 'none', NULL, 0, NULL, 1),
 ('aaaaaaaa-0000-0000-0000-000000000003', NULL, 'web@x.ru', NULL, NULL, 'en', false, false, false, now() - interval '10 days', 'WEB33', 'cold', NULL, 0, NULL, 1);
INSERT INTO referral (id, referrer_id, referee_id, used_at, bonus_granted, bot_id) VALUES (1, 'aaaaaaaa-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000002', now() - interval '100 days', true, 1);
INSERT INTO payment_method (id, customer_id, type, title, provider, provider_payment_method_id, is_active, created_at, bot_id)
VALUES ('pm-1', 'aaaaaaaa-0000-0000-0000-000000000001', 'sbp', 'СБП · Т-Банк', 'yookassa', '2d7c-yk', true, now() - interval '190 days', 1);
INSERT INTO subscription (id, customer_id, name, remnawave_id, remnawave_uuid, subscription_link, expire_at, plan_id, is_primary, devices_count, extra_devices,
  autopay_enabled, autopay_months, autopay_period_unit, kind, created_at, updated_at, bot_id) VALUES
 (10, 'aaaaaaaa-0000-0000-0000-000000000001', '🟤 Бобрёнок', 501, '22222222-2222-2222-2222-222222222222', 'https://sub/x', now() + interval '20 days', 1, true, 4, 1, true, 3, NULL, 'paid', now() - interval '190 days', now(), 1),
 (11, 'aaaaaaaa-0000-0000-0000-000000000002', 'Пробный', 502, NULL, 'https://sub/y', now() - interval '90 days', NULL, true, 3, 0, false, 0, NULL, 'trial', now() - interval '100 days', now(), 1);
INSERT INTO purchase (id, customer_id, amount, original_amount, currency, status, operation_type, invoice_type, period, period_unit, plan_id, subscription_id,
  provider_id, created_at, paid_at, receipt_moy_nalog_id, bot_id) VALUES
 (1, 'aaaaaaaa-0000-0000-0000-000000000001', 299.00, 299.00, 'RUB', 'paid', 'purchase', 'yookassa', 1, NULL, 1, 10, 'yk-1', now() - interval '190 days', now() - interval '190 days', 'r1', 1),
 (2, 'aaaaaaaa-0000-0000-0000-000000000001', 756.00, 840.00, 'RUB', 'paid', 'purchase', 'yookassa', 3, NULL, 1, 10, 'yk-2', now() - interval '160 days', now() - interval '160 days', NULL, 1),
 (3, 'aaaaaaaa-0000-0000-0000-000000000001', 840.00, 840.00, 'RUB', 'paid', 'autopay', 'yookassa', 3, NULL, 1, 10, 'yk-3', now() - interval '70 days', now() - interval '70 days', NULL, 1),
 (4, 'aaaaaaaa-0000-0000-0000-000000000002', 299.00, 299.00, 'RUB', 'cancel', 'purchase', 'yookassa', 1, NULL, 1, NULL, 'yk-4', now() - interval '95 days', NULL, NULL, 1),
 (5, 'aaaaaaaa-0000-0000-0000-000000000001', 60.00, 60.00, 'RUB', 'paid', 'traffic', 'yookassa', 0, NULL, 8, 10, 'yk-5', now() - interval '20 days', now() - interval '20 days', NULL, 1);
INSERT INTO referral_bonus_history (id, referral_id, purchase_id, bonus_days, is_first_bonus, granted_at, is_referee, bonus_type) VALUES
 (1, 1, NULL, 3, false, now() - interval '100 days', true, 'referee'),
 (2, 1, 2, 3, true, now() - interval '160 days', false, 'first'),
 (3, 1, NULL, 7, false, now() - interval '50 days', false, 'manual');
INSERT INTO promo (id, code, bonus_days, max_uses, used_count, active, created_at, promo_type, discount_percent, allowed_plans, bot_id) VALUES
 (1, 'BOBR10', 0, 100, 1, true, now() - interval '170 days', 'discount', 10, '[1]', 1),
 (2, 'GIFT7', 7, 0, 0, true, now(), 'bonus', 0, NULL, 1),
 (3, 'OPEN', 0, 0, 0, true, now(), 'unlock_plan', 0, NULL, 1);
INSERT INTO promo_usage (id, promo_id, customer_id, subscription_id, used_at) VALUES (1, 1, 'aaaaaaaa-0000-0000-0000-000000000001', 10, now() - interval '160 days');
INSERT INTO device_names (subscription_id, hwid, readable_name) VALUES (10, 'hw-1', 'Мой айфон');
INSERT INTO faq_sections (id, title, sort_order, is_active, bot_id) VALUES (1, 'Подключение', 1, true, 1);
INSERT INTO faq (id, question, answer, sort_order, is_active, section_id, bot_id) VALUES (1, 'Как подключиться?', 'Откройте раздел «Установка».', 1, true, 1, 1);
INSERT INTO support_ticket (id, subject, status, created_at, updated_at, closed_at, customer_id, bot_id) VALUES
 (1, 'Не работает', 'closed', now() - interval '30 days', now() - interval '29 days', now() - interval '29 days', 'aaaaaaaa-0000-0000-0000-000000000001', 1),
 (2, 'Вопрос', 'in_progress', now() - interval '1 day', now(), NULL, 'aaaaaaaa-0000-0000-0000-000000000002', 1);
INSERT INTO support_message (id, ticket_id, sender_type, message, created_at) VALUES
 (1, 1, 'user', 'Не работает VPN', now() - interval '30 days'), (2, 1, 'bot', 'Попробуйте переподключиться', now() - interval '30 days'),
 (3, 1, 'admin', 'Починили', now() - interval '29 days'), (4, 2, 'user', 'Как продлить?', now() - interval '1 day');
`

func connect(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := db.Open(context.Background(), url)
	if err != nil {
		t.Skip("нет Postgres:", err)
	}
	return p
}

func TestImportRWP(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	admin := connect(t, base)
	defer admin.Close()
	for _, q := range []string{`DROP DATABASE IF EXISTS bshop_rwp_src`, `DROP DATABASE IF EXISTS bshop_rwp_dst`, `CREATE DATABASE bshop_rwp_src`, `CREATE DATABASE bshop_rwp_dst`} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	srcURL := strings.Replace(base, "/bshop_test", "/bshop_rwp_src", 1)
	dstURL := strings.Replace(base, "/bshop_test", "/bshop_rwp_dst", 1)
	src := connect(t, srcURL)
	defer src.Close()
	dst := connect(t, dstURL)
	defer dst.Close()
	ddl, err := os.ReadFile("testdata/rwp_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, sample); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, dst); err != nil {
		t.Fatal(err)
	}

	// Dry run writes nothing.
	rep, err := importer.Run(ctx, src, dst, importer.Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	_ = dst.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&n)
	if n != 0 || rep.Counts["клиенты"] != 3 {
		t.Fatalf("dry run: customers=%d report=%v", n, rep.Counts)
	}

	rep, err = importer.Run(ctx, src, dst, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + rep.String())
	if len(rep.Unknown) != 0 {
		t.Fatalf("unknown values: %v", rep.Unknown)
	}
	q := func(sql string, args ...any) int64 {
		var v int64
		if err := dst.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}
	checks := []struct {
		name string
		sql  string
		want int64
	}{
		{"customers", `SELECT count(*) FROM customers`, 3},
		{"email lowercased", `SELECT count(*) FROM customers WHERE email='a@mail.ru'`, 1},
		{"identities", `SELECT count(*) FROM auth_identities`, 5}, // tg×2, email×2, yandex
		{"referrer", `SELECT count(*) FROM customers c JOIN customers r ON r.id=c.referrer_id WHERE c.username='bob' AND r.username='alex'`, 1},
		{"bot blocked", `SELECT count(*) FROM customers WHERE username='bob' AND bot_blocked`, 1},
		{"lead none → empty", `SELECT count(*) FROM customers WHERE username='bob' AND lead_temperature=''`, 1},
		{"plans", `SELECT count(*) FROM plans`, 3},
		{"periods in kopecks", `SELECT count(*) FROM plan_periods WHERE price=84000 AND amount=3 AND unit='month' AND is_best`, 1},
		{"extra device price", `SELECT extra_device_price FROM plans WHERE name='🟤 Бобрёнок'`, 5000},
		{"squads", `SELECT cardinality(rw_internal_squads) FROM plans WHERE name='🟤 Бобрёнок'`, 1},
		{"traffic GB", `SELECT traffic_limit_gb FROM plans WHERE name='🟣 Бобр Мастер'`, 75},
		{"subscriptions", `SELECT count(*) FROM subscriptions`, 2},
		{"autopay kept with card", `SELECT count(*) FROM subscriptions WHERE autopay AND autopay_method_id IS NOT NULL AND autopay_period_id IS NOT NULL`, 1},
		{"trial kind", `SELECT count(*) FROM subscriptions WHERE kind='trial' AND rw_user_id=502`, 1},
		{"payments", `SELECT count(*) FROM payments`, 5},
		{"buy then renew", `SELECT count(*) FROM payments WHERE operation='renew' AND amount=75600 AND base_amount=84000 AND discount_percent=10 AND months=3`, 1},
		{"first is buy", `SELECT count(*) FROM payments WHERE operation='buy' AND status='succeeded'`, 1},
		{"autopay op", `SELECT count(*) FROM payments WHERE operation='autopay' AND autopay`, 1},
		{"canceled", `SELECT count(*) FROM payments WHERE status='canceled'`, 1},
		{"traffic op", `SELECT count(*) FROM payments WHERE operation='traffic' AND months=0`, 1},
		{"receipt", `SELECT count(*) FROM payments WHERE receipt_status='done' AND receipt_uuid='r1'`, 1},
		{"promos (unlock skipped)", `SELECT count(*) FROM promo_codes`, 2},
		{"promo plans", `SELECT count(*) FROM promo_codes WHERE code='BOBR10' AND kind='discount' AND value=10 AND cardinality(plan_ids)=1`, 1},
		{"redemptions", `SELECT count(*) FROM promo_redemptions`, 1},
		{"bonuses", `SELECT count(*) FROM referral_bonuses`, 3},
		{"invitee bonus to referee", `SELECT count(*) FROM referral_bonuses b JOIN customers c ON c.id=b.recipient_id WHERE b.kind='invitee' AND c.username='bob'`, 1},
		{"first bonus linked to payment", `SELECT count(*) FROM referral_bonuses WHERE kind='first' AND payment_id IS NOT NULL`, 1},
		{"device names", `SELECT count(*) FROM device_names`, 1},
		{"faq", `SELECT count(*) FROM faq_items WHERE section='Подключение'`, 1},
		{"tickets", `SELECT count(*) FROM tickets`, 2},
		{"open ticket", `SELECT count(*) FROM tickets WHERE status='open'`, 1},
		{"messages", `SELECT count(*) FROM ticket_messages`, 4},
		{"ai message", `SELECT count(*) FROM ticket_messages WHERE author='ai'`, 1},
	}
	for _, c := range checks {
		if got := q(c.sql); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	// A second run into a non-empty base is refused.
	if _, err := importer.Run(ctx, src, dst, importer.Options{}); err == nil {
		t.Fatal("second import should be refused")
	}
}
