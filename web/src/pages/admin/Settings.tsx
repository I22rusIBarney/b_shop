import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { get, post, put, type Plan } from '../../lib/api'
import { Close, Plus } from '../../ui/icons'
import { ErrorBox, Field, Spinner, Toggle, useAction, useLoad } from '../../ui/kit'

type FieldSpec =
  | { k: string; label: string; hint?: string; type: 'text' | 'secret' | 'textarea' | 'number' | 'bool' | 'ids' | 'strings' | 'plan' | 'methods' | 'subpage' }
  | { k: string; label: string; hint?: string; type: 'select'; options: [string, string][] }

interface SectionSpec {
  key: string
  title: string
  about?: string
  test?: string
  fields: FieldSpec[]
  custom?: 'tiers' | 'links' | 'apps'
}

const pages: Record<string, { title: string; sections: SectionSpec[] }> = {
  general: {
    title: 'Общие',
    sections: [
      {
        key: 'general',
        title: 'Магазин',
        fields: [
          { k: 'shop_name', label: 'Название', type: 'text' },
          { k: 'timezone', label: 'Часовой пояс', hint: 'Например Asia/Barnaul. Влияет на тихие часы и статистику', type: 'text' },
          { k: 'days_in_month', label: 'Дней в месяце', hint: 'Сколько дней даёт «1 месяц» подписки', type: 'number' },
          { k: 'max_subs_per_customer', label: 'Подписок на клиента', hint: '0 — без ограничений', type: 'number' },
          { k: 'traffic_reset_threshold', label: 'Порог кнопки «Сбросить трафик», %', type: 'number' },
          { k: 'blocked_telegram_ids', label: 'Заблокированные Telegram ID', hint: 'Через запятую', type: 'ids' },
          { k: 'maintenance_mode', label: 'Режим обслуживания', hint: 'Клиенты увидят заглушку, администраторы — всё как обычно', type: 'bool' },
        ],
      },
    ],
  },
  payments: {
    title: 'Оплата и ЮKassa',
    sections: [
      {
        key: 'yookassa',
        title: 'ЮKassa',
        about: 'Адрес для уведомлений в личном кабинете ЮKassa: /api/webhooks/yookassa на вашем домене. События: payment.succeeded, payment.canceled, refund.succeeded.',
        test: 'yookassa',
        fields: [
          { k: 'enabled', label: 'Принимать оплату через ЮKassa', type: 'bool' },
          { k: 'shop_id', label: 'Shop ID', type: 'text' },
          { k: 'secret_key', label: 'Секретный ключ', type: 'secret' },
          { k: 'methods', label: 'Способы оплаты', type: 'methods' },
          { k: 'save_card', label: 'Разрешить сохранять карту', hint: 'Нужно для автопродления', type: 'bool' },
          { k: 'commission_percent', label: 'Комиссия сверху, %', hint: 'Добавляется к цене при оплате. 0 — не добавлять', type: 'number' },
          { k: 'description_template', label: 'Описание платежа', hint: '{plan} — тариф, {period} — срок, {days}, {months}', type: 'text' },
          { k: 'api_url', label: 'Адрес API', type: 'text' },
        ],
      },
      {
        key: 'payments',
        title: 'Платежи и автопродление',
        fields: [
          { k: 'pending_minutes', label: 'Сколько минут ждать оплату', hint: 'Потом счёт отменяется', type: 'number' },
          { k: 'success_url', label: 'Страница после оплаты', hint: 'Пусто — встроенная страница статуса', type: 'text' },
          { k: 'autopay_enabled', label: 'Автопродление сохранённым способом оплаты', type: 'bool' },
          { k: 'autopay_days_before', label: 'Списывать за, дней до окончания', type: 'number' },
          { k: 'autopay_notify_days', label: 'Предупреждать за, дней до списания', type: 'number' },
          { k: 'show_save_card', label: 'Показывать «Запомнить карту»', type: 'bool' },
        ],
      },
    ],
  },
  moynalog: {
    title: 'Мой налог',
    sections: [
      {
        key: 'moynalog',
        title: 'Чеки самозанятого',
        about: 'После каждой оплаты чек регистрируется в «Мой налог» с датой платежа. При ошибке отправка повторяется, при возврате чек аннулируется.',
        test: 'moynalog',
        fields: [
          { k: 'enabled', label: 'Отправлять чеки', type: 'bool' },
          { k: 'login', label: 'ИНН или телефон', type: 'text' },
          { k: 'password', label: 'Пароль от lknpd.nalog.ru', type: 'secret' },
          { k: 'template', label: 'Название услуги в чеке', hint: '{plan} — тариф, {months} — месяцев, {days} — дней, {operation} — покупка/продление', type: 'text' },
          { k: 'api_url', label: 'Адрес API', type: 'text' },
        ],
      },
    ],
  },
  auth: {
    title: 'Вход и почта',
    sections: [
      {
        key: 'auth',
        title: 'Способы входа',
        about: 'Для Яндекс ID и VK ID укажите адрес возврата в настройках приложения: /api/auth/oauth/yandex/callback и /api/auth/oauth/vk/callback на вашем домене.',
        fields: [
          { k: 'telegram_enabled', label: 'Telegram', type: 'bool' },
          { k: 'email_enabled', label: 'Код на почту', type: 'bool' },
          { k: 'yandex_enabled', label: 'Яндекс ID', type: 'bool' },
          { k: 'yandex_client_id', label: 'Яндекс: Client ID', type: 'text' },
          { k: 'yandex_secret', label: 'Яндекс: Client secret', type: 'secret' },
          { k: 'vk_enabled', label: 'VK ID', type: 'bool' },
          { k: 'vk_client_id', label: 'VK: ID приложения', type: 'text' },
          { k: 'vk_secret', label: 'VK: защищённый ключ', hint: 'Необязательно', type: 'secret' },
          { k: 'session_days', label: 'Сколько дней помнить вход', type: 'number' },
          { k: 'otp_ttl_seconds', label: 'Код действует, секунд', type: 'number' },
          { k: 'otp_max_attempts', label: 'Попыток ввода кода', type: 'number' },
        ],
      },
      {
        key: 'smtp',
        title: 'Почта (SMTP)',
        about: 'Через эту почту уходят коды входа и уведомления. Тестовое письмо придёт на почту, привязанную к вашему аккаунту.',
        test: 'smtp',
        fields: [
          { k: 'host', label: 'Сервер', hint: 'Например mail.hosting.reg.ru', type: 'text' },
          { k: 'port', label: 'Порт', hint: '587 — STARTTLS, 465 — SSL', type: 'number' },
          { k: 'username', label: 'Логин', type: 'text' },
          { k: 'password', label: 'Пароль', type: 'secret' },
          { k: 'from', label: 'Адрес отправителя', type: 'text' },
          { k: 'from_name', label: 'Имя отправителя', type: 'text' },
          { k: 'helo', label: 'HELO', hint: 'Обычно домен отправителя', type: 'text' },
          { k: 'rate_per_second', label: 'Писем в секунду', type: 'number' },
          { k: 'proxy_url', label: 'SOCKS5-прокси', hint: 'Если хостинг закрывает SMTP-порты: socks5://user:pass@host:port', type: 'text' },
        ],
      },
    ],
  },
  trial: {
    title: 'Пробный период',
    sections: [
      {
        key: 'trial',
        title: 'Пробный период',
        about: 'Новый клиент получает подписку на тарифе с отметкой «Для пробного периода». Выдаётся один раз.',
        fields: [
          { k: 'enabled', label: 'Выдавать пробный период', type: 'bool' },
          { k: 'plan_id', label: 'Тариф', type: 'plan' },
          { k: 'days', label: 'Дней', type: 'number' },
          { k: 'hide_plan_change', label: 'Скрыть смену тарифа на пробном', type: 'bool' },
        ],
      },
    ],
  },
  referral: {
    title: 'Рефералка',
    sections: [
      {
        key: 'referral',
        title: 'Реферальная программа',
        custom: 'tiers',
        fields: [
          { k: 'enabled', label: 'Включена', type: 'bool' },
          { k: 'base_enabled', label: 'Основные бонусы', hint: 'Начисляются при первой покупке приглашённого', type: 'bool' },
          { k: 'inviter_days', label: 'Пригласившему, дней', type: 'number' },
          { k: 'invitee_days', label: 'Приглашённому, дней', hint: '0 — не начислять', type: 'number' },
          { k: 'recurring_enabled', label: 'Бонус с каждой покупки приглашённого', type: 'bool' },
          { k: 'recurring_percent', label: 'Процент от дней покупки', hint: 'Минимум 1 день, только пригласившему', type: 'number' },
          { k: 'tiers_enabled', label: 'Уровни', hint: 'Бонус пригласившего растёт с числом оплативших приглашённых', type: 'bool' },
          { k: 'description', label: 'Описание для клиентов', type: 'textarea' },
        ],
      },
      {
        key: 'gifts',
        title: 'Подарки и покупка другому',
        fields: [
          { k: 'enabled', label: 'Подарочные подписки', hint: 'Клиент оплачивает тариф и получает код GIFT-…; активировать его может любой, один раз', type: 'bool' },
          { k: 'buy_for_other', label: 'Покупка для другого', hint: 'Клиент оплачивает подписку на чужой аккаунт по Telegram ID, @username или email', type: 'bool' },
        ],
      },
    ],
  },
  links: {
    title: 'Ссылки и установка',
    sections: [
      { key: 'links', title: 'Ссылки в кабинете и боте', about: 'Канал, правила, поддержка и любые другие ссылки.', custom: 'links', fields: [] },
      {
        key: 'install',
        title: 'Инструкции по подключению',
        about: 'По умолчанию приложения и пошаговые инструкции берутся из панели Remnawave (Настройки страницы подписки) — меняете там, кабинет подхватывает в течение 5 минут. Вручную: список ниже, в ссылке добавления {url} — ссылка подписки, {url_b64} — она же в base64.',
        custom: 'apps',
        fields: [
          { k: 'source', label: 'Откуда брать', type: 'select', options: [['remnawave', 'Из панели Remnawave'], ['manual', 'Вручную']] },
          { k: 'subpage_config_uuid', label: 'Конфигурация из панели', type: 'subpage' },
          { k: 'note', label: 'Примечание под инструкцией', type: 'textarea' },
        ],
      },
    ],
  },
  support: {
    title: 'Поддержка и ассистент',
    sections: [
      {
        key: 'support',
        title: 'Обращения',
        about: 'Таймеры считаются от последнего изменения обращения. Нерабочее время — в часовом поясе магазина.',
        fields: [
          { k: 'enabled', label: 'Принимать обращения в кабинете', hint: 'Выключено — в разделе «Поддержка» останется ссылка на поддержку', type: 'bool' },
          { k: 'bot_chat', label: 'Сообщения боту попадают в обращения', hint: 'Клиент пишет боту — ответ оператора и ассистента приходит туда же', type: 'bool' },
          { k: 'max_open', label: 'Открытых обращений на клиента', hint: '0 — без ограничений', type: 'number' },
          { k: 'autoclose_waiting_minutes', label: 'Закрывать без ответа клиента через, мин', hint: '0 — не закрывать', type: 'number' },
          { k: 'autoclose_resolved_minutes', label: 'Закрывать решённые через, мин', type: 'number' },
          { k: 'email_reply_delay_minutes', label: 'Письмо об ответе через, мин', hint: 'Для клиентов без Telegram. Не отправляется, если клиент уже ответил', type: 'number' },
          { k: 'attach_max_mb', label: 'Размер вложения, МБ', type: 'number' },
          { k: 'attach_max_files', label: 'Вложений в сообщении', hint: '0 — вложения выключены', type: 'number' },
          { k: 'rating_enabled', label: 'Просить оценку после закрытия', type: 'bool' },
          { k: 'rating_delay_minutes', label: 'Просить оценку через, мин после закрытия', type: 'number' },
          { k: 'work_hours', label: 'Рабочие часы', hint: 'Например 10:00-22:00. Пусто — всегда', type: 'text' },
          { k: 'new_ticket_text', label: 'Автоответ на новое обращение', type: 'textarea' },
          { k: 'off_hours_text', label: 'Автоответ в нерабочее время', type: 'textarea' },
        ],
      },
      {
        key: 'ai',
        title: 'ИИ-ассистент (первая линия)',
        about: 'Любой OpenAI-совместимый API: OpenRouter (https://openrouter.ai/api/v1) или свой прокси к Gemini. Ассистент отвечает сам, если уверен; иначе оставляет оператору черновик. Деньги, возвраты и просьбы позвать человека он передаёт оператору.',
        test: 'ai',
        fields: [
          { k: 'enabled', label: 'Ассистент отвечает в обращениях', type: 'bool' },
          { k: 'base_url', label: 'Адрес API (Base URL)', hint: 'Запросы идут на {адрес}/chat/completions', type: 'text' },
          { k: 'api_key', label: 'API-ключ', type: 'secret' },
          { k: 'model', label: 'Модель', hint: 'Например google/gemini-2.5-flash или gemini-2.5-flash', type: 'text' },
          { k: 'fallback_models', label: 'Резервные модели', hint: 'Через запятую, пробуются по очереди', type: 'strings' },
          { k: 'vision_model', label: 'Модель для картинок', hint: 'Пусто — основная', type: 'text' },
          { k: 'reasoning_effort', label: 'Рассуждение (reasoning)', type: 'select', options: [['', 'По умолчанию'], ['low', 'Низкое'], ['medium', 'Среднее'], ['high', 'Высокое']] },
          { k: 'confidence', label: 'Порог уверенности', hint: 'От 0 до 1. Ниже — черновик оператору', type: 'number' },
          { k: 'debounce_seconds', label: 'Ждать новых сообщений, сек', hint: 'Клиент часто пишет несколькими сообщениями', type: 'number' },
          { k: 'max_calls_per_hour', label: 'Ответов на обращение в час', type: 'number' },
          { k: 'history_messages', label: 'Сообщений истории', type: 'number' },
          { k: 'max_tokens', label: 'Токенов в ответе', type: 'number' },
          { k: 'temperature', label: 'Temperature', type: 'number' },
          { k: 'timeout_seconds', label: 'Таймаут, сек', type: 'number' },
          { k: 'knowledge_chars', label: 'Лимит базы знаний, символов', type: 'number' },
          { k: 'analyze_images', label: 'Смотреть картинки из обращения', type: 'bool' },
          { k: 'use_customer_data', label: 'Давать данные клиента (подписки, тарифы, цены)', type: 'bool' },
          { k: 'use_knowledge', label: 'Давать частые вопросы и базу знаний', type: 'bool' },
          { k: 'notify_drafts', label: 'Уведомлять о черновиках', type: 'bool' },
          { k: 'system_prompt', label: 'Инструкция ассистенту', type: 'textarea' },
        ],
      },
    ],
  },
  automations: {
    title: 'Автоматизации и рассылки',
    sections: [
      {
        key: 'automations',
        title: 'Автоматизации',
        about: 'Общий выключатель и пороги триггеров. Сами сценарии — в разделе «Автоматизации».',
        fields: [
          { k: 'enabled', label: 'Автоматизации работают', type: 'bool' },
          { k: 'quiet_hours', label: 'Тихие часы', hint: 'Например 22:00-09:00 — сообщения подождут до утра. Пусто — без тихих часов', type: 'text' },
          { k: 'expiring_soon_days', label: '«Скоро истекает»: за сколько дней', type: 'number' },
          { k: 'recently_expired_hours', label: '«Недавно истекла»: в течение, часов', type: 'number' },
          { k: 'abandoned_cart_hours', label: '«Брошенная корзина»: счёт за последние, часов', type: 'number' },
          { k: 'active_trial_hours', label: '«Пробный период»: начат в течение, часов', type: 'number' },
          { k: 'inactive_days', label: '«Давно не заходил»: дней', type: 'number' },
          { k: 'hot_seen_days', label: 'Горячий лид: заходил за, дней', hint: 'Не платил, но пытался оплатить', type: 'number' },
          { k: 'warm_seen_days', label: 'Тёплый лид: заходил за, дней', hint: 'Не платил, брал пробный период', type: 'number' },
          { k: 'cool_seen_days', label: 'Прохладный лид: заходил за, дней', hint: 'Дольше — холодный', type: 'number' },
          { k: 'lead_min_age_hours', label: 'Не считать лидом первые, часов', type: 'number' },
          { k: 'stable_min_payments', label: 'Стабильный клиент: оплат от', type: 'number' },
          { k: 'stable_min_days', label: 'Стабильный клиент: платит дней', type: 'number' },
          { k: 'personal_promo_hours', label: 'Личный промокод {promo:…} действует, часов', type: 'number' },
        ],
      },
      {
        key: 'broadcasts',
        title: 'Рассылки',
        fields: [{ k: 'rate_per_second', label: 'Сообщений в секунду', hint: 'Telegram допускает до 30', type: 'number' }],
      },
    ],
  },
}

const methodNames: [string, string][] = [
  ['sbp', 'СБП'],
  ['bank_card', 'Карта'],
  ['sberbank', 'SberPay'],
  ['tinkoff_bank', 'T-Pay'],
  ['yoo_money', 'ЮMoney'],
]

export default function SettingsPage() {
  const { section = 'general' } = useParams()
  const page = pages[section]
  if (!page) return <ErrorBox text="Раздел не найден" />
  return (
    <div className="stack lg" style={{ maxWidth: 900 }}>
      <h1>{page.title}</h1>
      {page.sections.map((s) => (
        <SectionForm key={s.key} spec={s} />
      ))}
    </div>
  )
}

function SectionForm({ spec }: { spec: SectionSpec }) {
  const data = useLoad(() => get<Record<string, any>>(`/api/admin/settings/${spec.key}`), [spec.key])
  const plans = useLoad(() => (spec.fields.some((f) => f.type === 'plan') ? get<Plan[]>('/api/admin/plans') : Promise.resolve([])), [spec.key])
  const subpages = useLoad(
    () => (spec.fields.some((f) => f.type === 'subpage') ? get<{ uuid: string; name: string }[]>('/api/admin/remnawave/subpage-configs').catch(() => []) : Promise.resolve([])),
    [spec.key],
  )
  const [v, setV] = useState<Record<string, any> | null>(null)
  const [test, setTest] = useState<{ ok: boolean; message: string } | null>(null)
  const { busy, run } = useAction()

  useEffect(() => {
    if (data.data) setV(structuredClone(data.data))
  }, [data.data])

  if (data.error) return <ErrorBox text={data.error} retry={data.reload} />
  if (!v)
    return (
      <div className="card center">
        <Spinner />
      </div>
    )
  const set = (k: string, val: any) => setV({ ...v, [k]: val })
  const dirty = JSON.stringify(v) !== JSON.stringify(data.data)

  const save = () =>
    run(async () => {
      const saved = await put<Record<string, any>>(`/api/admin/settings/${spec.key}`, v)
      data.setData(saved)
    }, 'Сохранено')

  const runTest = () =>
    run(async () => {
      if (dirty) await save()
      setTest(await post<{ ok: boolean; message: string }>(`/api/admin/settings/${spec.test}/test`))
    })

  return (
    <section className="card stack">
      <h2>{spec.title}</h2>
      {spec.about && <p className="small muted">{spec.about}</p>}
      <div className="form-grid">
        {spec.fields
          .filter((f) => f.type !== 'bool' && f.type !== 'textarea' && f.type !== 'methods')
          .map((f) => (
            <Field key={f.k} label={f.label} hint={f.hint}>
              {f.type === 'select' ? (
                <select className="input" value={v[f.k]} onChange={(e) => set(f.k, e.target.value)}>
                  {f.options.map(([val, name]) => (
                    <option key={val} value={val}>
                      {name}
                    </option>
                  ))}
                </select>
              ) : f.type === 'plan' ? (
                <select className="input" value={v[f.k] || 0} onChange={(e) => set(f.k, Number(e.target.value))}>
                  <option value={0}>Не выбран</option>
                  {(plans.data || []).map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                      {p.is_trial ? ' (пробный)' : ''}
                    </option>
                  ))}
                </select>
              ) : f.type === 'subpage' ? (
                <select className="input" value={v[f.k] || ''} onChange={(e) => set(f.k, e.target.value)} disabled={v.source === 'manual'}>
                  <option value="">Первая по порядку</option>
                  {(subpages.data || []).map((c) => (
                    <option key={c.uuid} value={c.uuid}>
                      {c.name}
                    </option>
                  ))}
                </select>
              ) : f.type === 'number' ? (
                <input className="input num" type="number" step="any" value={v[f.k] ?? 0} onChange={(e) => set(f.k, Number(e.target.value))} />
              ) : f.type === 'strings' ? (
                <input className="input" value={(v[f.k] || []).join(', ')} onChange={(e) => set(f.k, e.target.value.split(',').map((x) => x.trim()).filter(Boolean))} />
              ) : f.type === 'ids' ? (
                <input className="input" value={(v[f.k] || []).join(', ')} onChange={(e) => set(f.k, e.target.value.split(/[\s,]+/).filter(Boolean).map(Number).filter((n) => !Number.isNaN(n)))} />
              ) : (
                <input className="input" type={f.type === 'secret' ? 'password' : 'text'} autoComplete="off" value={v[f.k] ?? ''} onChange={(e) => set(f.k, e.target.value)} onFocus={(e) => f.type === 'secret' && e.target.select()} />
              )}
            </Field>
          ))}
      </div>
      {spec.fields
        .filter((f) => f.type === 'methods')
        .map((f) => (
          <Field key={f.k} label={f.label}>
            <div className="row wrap">
              {methodNames.map(([code, name]) => {
                const on = (v[f.k] || []).includes(code)
                return (
                  <button key={code} className={on ? 'chip on' : 'chip'} onClick={() => set(f.k, on ? v[f.k].filter((x: string) => x !== code) : [...(v[f.k] || []), code])}>
                    {name}
                  </button>
                )
              })}
            </div>
          </Field>
        ))}
      {spec.fields
        .filter((f) => f.type === 'textarea')
        .map((f) => (
          <Field key={f.k} label={f.label} hint={f.hint}>
            <textarea className="input" value={v[f.k] ?? ''} onChange={(e) => set(f.k, e.target.value)} />
          </Field>
        ))}
      <div className="stack">
        {spec.fields
          .filter((f) => f.type === 'bool')
          .map((f) => (
            <Toggle key={f.k} checked={!!v[f.k]} onChange={(x) => set(f.k, x)} label={f.label} hint={f.hint} />
          ))}
      </div>

      {spec.custom === 'tiers' && <Tiers value={v.tiers || []} onChange={(t) => set('tiers', t)} />}
      {spec.custom === 'links' && <LinksEditor value={v.items || []} onChange={(t) => set('items', t)} />}
      {spec.custom === 'apps' && v.source === 'manual' && <AppsEditor value={v.apps || []} onChange={(t) => set('apps', t)} />}

      {test && <p className={test.ok ? 'small' : 'err'} style={test.ok ? { color: 'var(--ok)' } : undefined}>{test.message}</p>}
      <div className="row wrap" style={{ justifyContent: 'flex-end' }}>
        {spec.test && (
          <button className="btn secondary small" disabled={busy} onClick={runTest}>
            Проверить подключение
          </button>
        )}
        <button className="btn small" disabled={busy || !dirty} onClick={save}>
          Сохранить
        </button>
      </div>
    </section>
  )
}

function Tiers({ value, onChange }: { value: { min_invited: number; days: number }[]; onChange: (v: { min_invited: number; days: number }[]) => void }) {
  return (
    <div className="field">
      <label>Уровни</label>
      <span className="hint">Бонус уровня заменяет основной бонус пригласившего, когда у него столько оплативших приглашённых.</span>
      {value.map((t, i) => (
        <div className="row tier-row" key={i}>
          <span className="small muted">от</span>
          <input className="input num" type="number" min={1} value={t.min_invited} onChange={(e) => onChange(value.map((x, j) => (j === i ? { ...x, min_invited: Number(e.target.value) } : x)))} aria-label="Приглашённых" />
          <span className="small muted">друзей →</span>
          <input className="input num" type="number" min={0} value={t.days} onChange={(e) => onChange(value.map((x, j) => (j === i ? { ...x, days: Number(e.target.value) } : x)))} aria-label="Дней" />
          <span className="small muted">дн.</span>
          <button className="icon-btn" onClick={() => onChange(value.filter((_, j) => j !== i))} aria-label="Удалить уровень">
            <Close />
          </button>
        </div>
      ))}
      <div>
        <button className="btn secondary small" onClick={() => onChange([...value, { min_invited: (value.at(-1)?.min_invited || 0) + 5, days: (value.at(-1)?.days || 3) + 2 }])}>
          <Plus /> Добавить уровень
        </button>
      </div>
    </div>
  )
}

type LinkItem = { title: string; url: string; enabled: boolean; kind: string }

function LinksEditor({ value, onChange }: { value: LinkItem[]; onChange: (v: LinkItem[]) => void }) {
  const upd = (i: number, p: Partial<LinkItem>) => onChange(value.map((x, j) => (j === i ? { ...x, ...p } : x)))
  return (
    <div className="stack">
      {value.map((l, i) => (
        <div className="row wrap card flat" key={i}>
          <input className="input" style={{ maxWidth: 180 }} placeholder="Название" value={l.title} onChange={(e) => upd(i, { title: e.target.value })} />
          <input className="input grow" style={{ minWidth: 200 }} placeholder="https://" value={l.url} onChange={(e) => upd(i, { url: e.target.value })} />
          <select className="input" style={{ width: 150 }} value={l.kind} onChange={(e) => upd(i, { kind: e.target.value })} aria-label="Тип">
            <option value="">Обычная</option>
            <option value="support">Поддержка</option>
            <option value="channel">Канал</option>
            <option value="rules">Правила</option>
            <option value="privacy">Конфиденциальность</option>
          </select>
          <Toggle checked={l.enabled} onChange={(x) => upd(i, { enabled: x })} label="" />
          <button className="icon-btn" onClick={() => onChange(value.filter((_, j) => j !== i))} aria-label="Удалить ссылку">
            <Close />
          </button>
        </div>
      ))}
      <div>
        <button className="btn secondary small" onClick={() => onChange([...value, { title: '', url: 'https://', enabled: true, kind: '' }])}>
          <Plus /> Добавить ссылку
        </button>
      </div>
    </div>
  )
}

type App = { platform: string; name: string; store_url: string; deep_link: string }
const platformNames: [string, string][] = [
  ['ios', 'iPhone'],
  ['android', 'Android'],
  ['windows', 'Windows'],
  ['macos', 'macOS'],
  ['linux', 'Linux'],
  ['tv', 'Телевизор'],
]

function AppsEditor({ value, onChange }: { value: App[]; onChange: (v: App[]) => void }) {
  const upd = (i: number, p: Partial<App>) => onChange(value.map((x, j) => (j === i ? { ...x, ...p } : x)))
  return (
    <div className="stack">
      {value.map((a, i) => (
        <div className="card flat stack" key={i}>
          <div className="row wrap">
            <select className="input" style={{ width: 150 }} value={a.platform} onChange={(e) => upd(i, { platform: e.target.value })}>
              {platformNames.map(([k, n]) => (
                <option key={k} value={k}>
                  {n}
                </option>
              ))}
            </select>
            <input className="input grow" placeholder="Название, например INCY" value={a.name} onChange={(e) => upd(i, { name: e.target.value })} />
            <button className="icon-btn" onClick={() => onChange(value.filter((_, j) => j !== i))} aria-label="Удалить приложение">
              <Close />
            </button>
          </div>
          <input className="input" placeholder="Ссылка на магазин приложений" value={a.store_url} onChange={(e) => upd(i, { store_url: e.target.value })} />
          <input className="input" placeholder="Ссылка добавления подписки, например happ://add/{url}" value={a.deep_link} onChange={(e) => upd(i, { deep_link: e.target.value })} />
        </div>
      ))}
      <div>
        <button className="btn secondary small" onClick={() => onChange([...value, { platform: 'ios', name: '', store_url: '', deep_link: '' }])}>
          <Plus /> Добавить приложение
        </button>
      </div>
    </div>
  )
}
