import CustomerExtras from './CustomerExtras'
import { CustomerGeo } from './Geo'
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { del, get, patch, post, type Customer, type Device, type Identity, type Payment, type Plan, type PlanChange, type Redemption, type ReferralBonus, type Subscription } from '../../lib/api'
import { bytes, date, dateTime, operationName, paymentStatus, providerName, relative, rub } from '../../lib/format'
import { Avatar, Empty, InfoRow, Panel, Switcher } from '../../ui/admin'
import { Calendar, Card, Clock, Coin, Copy, Gift, Key, Mail, Note, Plus, Send, Swap, Tag, Trash, Trend, Trial, Users, User } from '../../ui/icons'
import { showBackButton } from '../../lib/tg'
import { ErrorBox, Field, Sheet, Spinner, Toggle, copy, useAction, useLoad, useToast } from '../../ui/kit'

interface Event {
  id: number
  kind: string
  data: Record<string, any>
  actor_id: number | null
  created_at: string
}

interface Detail {
  customer: Customer
  subscriptions: Subscription[]
  identities: Identity[]
  payments: Payment[]
  events: Event[]
  referrer: Customer | null
  promos: Redemption[] | null
  bonuses: ReferralBonus[] | null
  plan_changes: PlanChange[] | null
  referral: { invited: number; paid: number; days: number }
  unlocked_plans: string[] | null
}

const bonusKind: Record<string, string> = { first: 'за первую оплату реферала', recurring: 'за продление реферала', invitee: 'приглашённому', manual: 'вручную' }
const modeLabel: Record<string, string> = { upgrade: 'доплата', convert: 'конвертация', downgrade: 'понижение', admin: 'админ' }

const eventName: Record<string, string> = {
  registered: 'Регистрация',
  login: 'Вход',
  login_linked: 'Привязан способ входа',
  payment_created: 'Создан платёж',
  purchase: 'Покупка',
  trial: 'Пробный период',
  refund: 'Возврат',
  admin_update: 'Изменён администратором',
  pricing_changed: 'Персональная цена',
  refund_partial: 'Частичный возврат',
  admin_days: 'Изменён срок',
  admin_grant: 'Выдана подписка',
  device_deleted: 'Удалено устройство',
  subscription_revoked: 'Перевыпущена ссылка',
  subscription_deleted: 'Удалена подписка',
  subscription_status: 'Статус подписки',
  traffic_reset: 'Сброшен трафик',
  settings_changed: 'Изменены настройки',
  promo: 'Промокод',
  plan_change: 'Смена тарифа',
  admin_plan_change: 'Тариф изменён администратором',
  autopay_on: 'Включено автопродление',
  autopay_off: 'Отключено автопродление',
  autopay_failed: 'Ошибка автоплатежа',
  referral_manual: 'Начислены бонусные дни',
  device_added: 'Новое устройство',
  device_removed: 'Устройство удалено',
  torrent: 'Торрент-трафик',
  panel_sync: 'Подписка из панели',
}

function priceRule(discount: number, markup: number) {
  if (markup > 0) return `наценка +${markup}%`
  if (discount > 0) return `скидка −${discount}%`
  return 'обычная цена'
}

type PriceMode = 'none' | 'discount' | 'markup'

function eventText(e: Event) {
  const d = e.data || {}
  switch (e.kind) {
    case 'purchase':
      return `${operationName[d.op] || d.op}: ${d.plan}, ${d.days} дн., ${rub(d.amount)}`
    case 'payment_created':
      return `${d.plan}, ${rub(d.amount)}`
    case 'admin_days':
      return `${d.days > 0 ? '+' : ''}${d.days} дн.`
    case 'admin_grant':
      return `${d.plan}, ${d.days} дн.`
    case 'trial':
    case 'referral_manual':
      return `${d.days} дн.`
    case 'promo':
      return `${d.code} (${d.kind === 'days' ? `+${d.value} дн.` : d.kind === 'unlock' ? `открыт ${(d.plans || []).join(', ')}` : `−${d.value}%`})`
    case 'plan_change':
      return `${d.from} → ${d.to}, ${modeLabel[d.mode] || d.mode}, ${d.days_before} → ${d.days_after} дн.`
    case 'admin_plan_change':
      return d.to
    case 'pricing_changed':
      return `${priceRule(d.discount_from, d.markup_from)} → ${priceRule(d.discount_to, d.markup_to)}`
    case 'refund':
    case 'refund_partial':
      return d.total && d.total !== d.amount ? `${rub(d.amount)}, всего ${rub(d.total)}` : rub(d.amount)
    case 'autopay_failed':
      return d.reason
    case 'device_added':
    case 'device_removed':
      return d.device
    case 'torrent':
      return `${d.server}, IP ${d.ip}`
    case 'login':
    case 'registered':
    case 'login_linked':
      return providerName[d.provider] || d.provider || ''
    default:
      return ''
  }
}

// samplePrice picks the cheapest active plan period to show what the rule does to a real price.
function samplePrice(plans: Plan[] | null | undefined) {
  let best: { plan: string; price: number } | null = null
  for (const pl of plans || []) {
    if (!pl.is_active || pl.is_trial) continue
    for (const per of pl.periods || []) {
      if (per.price > 0 && (!best || per.price < best.price)) best = { plan: pl.name, price: per.price }
    }
  }
  return best
}

// Same rounding as the server: markup rounds up to the kopeck, discount rounds down.
const withMarkup = (k: number, pct: number) => (pct > 0 ? Math.floor((k * (100 + pct) + 99) / 100) : k)
const withDiscount = (k: number, pct: number) => (pct >= 100 ? 0 : pct > 0 ? Math.floor((k * (100 - pct)) / 100) : k)

function PersonalPrice({
  mode,
  onMode,
  percent,
  onPercent,
  sample,
}: {
  mode: PriceMode
  onMode: (m: PriceMode) => void
  percent: number
  onPercent: (v: number) => void
  sample: { plan: string; price: number } | null
}) {
  const max = mode === 'markup' ? 300 : 100
  const after = sample ? (mode === 'markup' ? withMarkup(sample.price, percent) : mode === 'discount' ? withDiscount(sample.price, percent) : sample.price) : 0
  return (
    <Field
      label="Персональная цена"
      hint={
        mode === 'markup'
          ? 'Наценка ложится на тарифы, продление, автопродление, смену тарифа, доп. устройства и подарки от этого клиента. Промокод даёт скидку уже от цены с наценкой. Трафик — по обычной цене. Уже созданные платежи не меняются.'
          : mode === 'discount'
            ? 'Скидка на те же покупки. Если у клиента промокод с большей скидкой — действует промокод, они не складываются.'
            : 'Клиент платит по прайсу тарифов.'
      }
    >
      <div className="price-rule">
        <Switcher
          label="Персональная цена"
          value={mode}
          onChange={(v) => onMode(v as PriceMode)}
          options={[
            { value: 'none', label: 'Обычная' },
            { value: 'discount', label: 'Скидка' },
            { value: 'markup', label: 'Наценка' },
          ]}
        />
        {mode !== 'none' && (
          <label className="input pct-field">
            <span>{mode === 'markup' ? '+' : '−'}</span>
            <input
              type="number"
              inputMode="numeric"
              min={0}
              max={max}
              value={percent}
              aria-label={mode === 'markup' ? 'Наценка, %' : 'Скидка, %'}
              onChange={(e) => onPercent(Math.max(0, Math.min(max, Math.round(Number(e.target.value) || 0))))}
            />
            <span>%</span>
          </label>
        )}
      </div>
      {sample && mode !== 'none' && percent > 0 && (
        <div className="price-sample small">
          <span className="muted">{sample.plan}:</span> <s className="muted num">{rub(sample.price)}</s> → <b className="num">{rub(after)}</b>
          <span className="muted"> · без комиссии кассы</span>
        </div>
      )}
    </Field>
  )
}

export default function CustomerCard() {
  const { id } = useParams()
  const nav = useNavigate()
  useEffect(() => showBackButton(() => nav('/admin/customers')), [nav])
  const toast = useToast()
  const data = useLoad(() => get<Detail>(`/api/admin/customers/${id}`), [id])
  const plans = useLoad(() => get<Plan[]>('/api/admin/plans'))
  const { busy, run } = useAction()
  const [notes, setNotes] = useState({ tag: '', notes: '', personal_discount: 0, personal_markup: 0 })
  const [priceMode, setPriceMode] = useState<PriceMode>('none')
  const [grantOpen, setGrantOpen] = useState(false)
  const [devicesFor, setDevicesFor] = useState<Subscription | null>(null)
  const [changeFor, setChangeFor] = useState<Subscription | null>(null)
  const [bonusDays, setBonusDays] = useState(3)
  const [promoCode, setPromoCode] = useState('')

  useEffect(() => {
    if (data.data) {
      const c = data.data.customer
      setNotes({ tag: c.tag, notes: c.notes, personal_discount: c.personal_discount, personal_markup: c.personal_markup })
      setPriceMode(c.personal_markup > 0 ? 'markup' : c.personal_discount > 0 ? 'discount' : 'none')
    }
  }, [data.data])

  if (data.error) return <ErrorBox text={data.error} retry={data.reload} />
  if (!data.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )
  const { customer: c, subscriptions, identities, payments, events, referrer } = data.data
  const promos = data.data.promos || []
  const bonuses = data.data.bonuses || []
  const changes = data.data.plan_changes || []
  const name = [c.first_name, c.last_name].filter(Boolean).join(' ') || (c.username ? `@${c.username}` : '') || c.email || `#${c.id}`

  const saveNotes = () =>
    run(async () => {
      // Only one rule at a time: the other percent is sent as 0.
      const body = {
        ...notes,
        personal_discount: priceMode === 'discount' ? notes.personal_discount : 0,
        personal_markup: priceMode === 'markup' ? notes.personal_markup : 0,
      }
      const d = await patch<Detail>(`/api/admin/customers/${c.id}`, body)
      data.setData(d)
    }, 'Сохранено')

  const flagQuestion: Record<string, string> = {
    'is_admin:true': 'Сделать клиента администратором? Он получит полный доступ к админке: клиенты, платежи, настройки, рассылки.',
    'is_admin:false': 'Снять права администратора?',
    'is_blocked:true': 'Заблокировать доступ? Клиент не сможет войти в кабинет и бот.',
  }
  const setFlag = (k: 'is_blocked' | 'is_admin', v: boolean) => {
    const q = flagQuestion[`${k}:${v}`]
    if (q && !confirm(q)) return
    return run(async () => {
      const d = await patch<Detail>(`/api/admin/customers/${c.id}`, { [k]: v })
      data.setData(d)
    })
  }

  const subAction = (fn: () => Promise<unknown>, ok: string) =>
    run(async () => {
      await fn()
      await data.reload()
    }, ok)

  const online = !!(c.last_seen_at && Date.now() - new Date(c.last_seen_at).getTime() < 10 * 60 * 1000)
  const copyText = async (v: string, what: string) => (await copy(v)) && toast(`${what} скопирован`)

  return (
    <div className="stack lg cc-page">
      <div className="cc-head admin-head" style={{ marginBottom: 0 }}>
        <Avatar name={name} seed={c.id} size={52} />
        <div className="cc-identity">
          <h1 className="ellipsis">{name}</h1>
          <div className="id-line">
            <span className="num">#{c.id}</span>
            {c.telegram_id && (
              <span className="num">
                TG {c.telegram_id}
                <button className="copy-btn" onClick={() => copyText(String(c.telegram_id), 'Telegram ID')} aria-label="Скопировать Telegram ID">
                  <Copy />
                </button>
              </span>
            )}
            {c.username && <span>@{c.username}</span>}
            {c.email && <span>{c.email}</span>}
          </div>
        </div>
        <div className="chips">
          <span className={online ? 'presence on' : 'presence'}>{online ? 'в сети' : `был ${relative(c.last_seen_at)}`}</span>
          {c.tag && <span className="badge accent">{c.tag}</span>}
          {c.is_admin && <span className="badge">админ</span>}
          {c.is_blocked && <span className="badge bad">заблокирован</span>}
          {c.bot_blocked && <span className="badge warn">заблокировал бота</span>}
        </div>
      </div>

      <div className="cc-grid">
        <Panel icon={User} tone="blue" title="Профиль">
          <div className="info-list">
            <InfoRow icon={Calendar} label="Регистрация">
              {dateTime(c.created_at)}
            </InfoRow>
            <InfoRow icon={Clock} label="Последний визит">
              {c.last_seen_at ? dateTime(c.last_seen_at) : '—'}
            </InfoRow>
            {identities.map((i) => (
              <InfoRow key={i.id} icon={i.provider.startsWith('telegram') ? Send : i.provider === 'email' ? Mail : Key} label={providerName[i.provider] || i.provider}>
                {i.display}
              </InfoRow>
            ))}
            <InfoRow icon={Trend} label="Источник">
              {c.source || '—'}
            </InfoRow>
            <InfoRow icon={Users} label="Пригласил">
              {referrer ? <Link to={`/admin/customers/${referrer.id}`}>{referrer.username ? `@${referrer.username}` : `#${referrer.id}`}</Link> : '—'}
            </InfoRow>
            <InfoRow icon={Tag} label="Реферальный код">
              <span className="num">{c.ref_code || '—'}</span>
            </InfoRow>
            <InfoRow icon={Trial} label="Пробный период">
              {c.trial_used ? 'использован' : 'не использован'}
            </InfoRow>
          </div>
        </Panel>

        <Panel icon={Note} tone="violet" title="Заметки администратора">
          <Field label="Тег">
            <input className="input" value={notes.tag} placeholder="VIP, проблемный, тест…" onChange={(e) => setNotes({ ...notes, tag: e.target.value })} />
          </Field>
          <PersonalPrice
            mode={priceMode}
            onMode={setPriceMode}
            percent={priceMode === 'markup' ? notes.personal_markup : notes.personal_discount}
            onPercent={(v) => setNotes(priceMode === 'markup' ? { ...notes, personal_markup: v } : { ...notes, personal_discount: v })}
            sample={samplePrice(plans.data)}
          />
          <Field label="Заметки">
            <textarea className="input" value={notes.notes} placeholder="Любые заметки о клиенте…" onChange={(e) => setNotes({ ...notes, notes: e.target.value })} />
          </Field>
          <div className="row">
            <button className="btn small" disabled={busy} onClick={saveNotes}>
              Сохранить
            </button>
          </div>
          <div className="info-list" style={{ marginTop: 4 }}>
            <Toggle checked={c.is_blocked} onChange={(v) => setFlag('is_blocked', v)} label="Заблокировать доступ" hint="Клиент не сможет войти в кабинет и бот" />
            <Toggle checked={c.is_admin} onChange={(v) => setFlag('is_admin', v)} label="Администратор" />
          </div>
        </Panel>

        <Panel
          icon={Key}
          tone="green"
          title="Подписки"
          count={subscriptions.length}
          wide
          actions={
            <button className="btn secondary small" onClick={() => setGrantOpen(true)}>
              <Plus /> Выдать
            </button>
          }
        >
          {subscriptions.length === 0 ? (
            <Empty icon={Key}>Подписок нет</Empty>
          ) : (
            subscriptions.map((s) => (
              <SubBlock
                key={s.id}
                s={s}
                busy={busy}
                onDays={(days) => subAction(() => post(`/api/admin/subscriptions/${s.id}/days`, { days }), 'Срок изменён')}
                onReset={() => confirm('Сбросить использованный трафик подписки?') && subAction(() => post(`/api/admin/subscriptions/${s.id}/reset-traffic`), 'Трафик сброшен')}
                onRevoke={() => confirm('Перевыпустить ссылку? Старая перестанет работать — клиенту нужно будет заново добавить подписку в приложение.') && subAction(() => post(`/api/subscriptions/${s.id}/revoke`, { only_passwords: false }), 'Ссылка перевыпущена')}
                onToggle={(en) => (en || confirm('Отключить подписку? VPN у клиента перестанет работать до включения.')) && subAction(() => post(`/api/admin/subscriptions/${s.id}/status`, { enabled: en }), en ? 'Подписка включена' : 'Подписка отключена')}
                onDelete={() => {
                  if (confirm(`Удалить подписку «${s.plan_name || s.title}» вместе с пользователем в панели?`)) subAction(() => del(`/api/admin/subscriptions/${s.id}`), 'Подписка удалена')
                }}
                onDevices={() => setDevicesFor(s)}
                onChange={() => setChangeFor(s)}
                onAutopay={() => confirm('Списать оплату продления с сохранённой карты клиента прямо сейчас?') && subAction(() => post(`/api/admin/subscriptions/${s.id}/autopay`), 'Автоплатёж запущен')}
                onCopy={async () => (await copy(s.subscription_url)) && toast('Ссылка скопирована')}
              />
            ))
          )}
        </Panel>

        <Panel icon={Coin} tone="amber" title="Платежи" count={payments.length}>
          {payments.length === 0 ? (
            <Empty icon={Card}>Платежей нет</Empty>
          ) : (
            <div className="feed">
              {payments.slice(0, 15).map((p) => (
                <div className="feed-item" key={p.id}>
                  <span className="ic" style={{ ['--c' as string]: p.status === 'succeeded' ? 'var(--ok)' : p.status === 'pending' ? 'var(--warn)' : undefined }}>
                    <Card />
                  </span>
                  <div className="main">
                    <div>
                      {operationName[p.operation] || p.operation} · {p.title}
                    </div>
                    <div className="s">
                      {dateTime(p.created_at)} · {p.method || p.provider}
                    </div>
                  </div>
                  <div className="end">
                    <b className="num">{rub(p.amount)}</b>
                    <span className={p.status === 'succeeded' ? 'badge ok' : p.status === 'pending' ? 'badge warn' : 'badge'}>{paymentStatus[p.status]}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Panel>

        <Panel icon={Gift} tone="rose" title="Бонусы и промокоды">
          <div className="stat-line">
            <span>
              Рефералов <b>{data.data.referral.invited}</b>
            </span>
            <span>
              оплатили <b>{data.data.referral.paid}</b>
            </span>
            <span>
              получено дней <b>{data.data.referral.days}</b>
            </span>
            {c.bonus_days > 0 && (
              <span>
                на балансе <b>{c.bonus_days} дн.</b>
              </span>
            )}
          </div>
          <div className="inline-form">
            <input className="input num" type="number" min={1} value={bonusDays} onChange={(e) => setBonusDays(Number(e.target.value))} style={{ width: 84 }} aria-label="Бонусных дней" />
            <button className="btn small" disabled={busy || bonusDays < 1} onClick={() => subAction(() => post(`/api/admin/customers/${c.id}/bonus`, { days: bonusDays }), 'Дни начислены')}>
              Начислить дни
            </button>
          </div>
          <form
            className="inline-form"
            onSubmit={(e) => {
              e.preventDefault()
              subAction(() => post(`/api/admin/customers/${c.id}/promo`, { code: promoCode }), 'Промокод применён').then(() => setPromoCode(''))
            }}
          >
            <input className="input" placeholder="ПРОМОКОД" value={promoCode} onChange={(e) => setPromoCode(e.target.value.toUpperCase())} style={{ width: 160 }} aria-label="Промокод" />
            <button className="btn secondary small" disabled={busy || !promoCode.trim()}>
              Применить за клиента
            </button>
          </form>
          {!!data.data.unlocked_plans?.length && <p className="small" style={{ margin: 0 }}>Открыты скрытые тарифы: {data.data.unlocked_plans.join(', ')}</p>}
          {promos.length + bonuses.length + changes.length === 0 ? (
            <Empty icon={Gift}>Бонусов, промокодов и смен тарифа не было</Empty>
          ) : (
            <div className="feed">
              {promos.map((r) => (
                <div className="feed-item" key={'p' + r.id}>
                  <span className="ic" style={{ ['--c' as string]: 'var(--accent)' }}>
                    <Tag />
                  </span>
                  <div className="main">
                    <div className="num">{r.code}</div>
                    <div className="s">{r.kind === 'days' ? `+${r.value} дн.` : r.kind === 'unlock' ? 'открытие тарифа' : `−${r.value}%`}</div>
                  </div>
                  <div className="end">
                    <span className={r.status === 'used' ? 'badge ok' : 'badge warn'}>{r.status === 'used' ? 'использован' : 'ждёт оплаты'}</span>
                    <span className="tiny muted">{date(r.created_at)}</span>
                  </div>
                </div>
              ))}
              {bonuses.map((b) => (
                <div className="feed-item" key={'b' + b.id}>
                  <span className="ic" style={{ ['--c' as string]: '#ff7a8a' }}>
                    <Gift />
                  </span>
                  <div className="main">
                    <div>+{b.days} дн.</div>
                    <div className="s">
                      {bonusKind[b.kind]}
                      {b.kind === 'first' || b.kind === 'recurring' ? ` · ${b.referral_name}` : ''}
                    </div>
                  </div>
                  <div className="end">
                    {b.pending && <span className="badge">на баланс</span>}
                    <span className="tiny muted">{date(b.created_at)}</span>
                  </div>
                </div>
              ))}
              {changes.map((x) => (
                <div className="feed-item" key={'c' + x.id}>
                  <span className="ic" style={{ ['--c' as string]: '#a98bff' }}>
                    <Swap />
                  </span>
                  <div className="main">
                    <div>
                      {x.from_name} → {x.to_name}
                    </div>
                    <div className="s">
                      {modeLabel[x.mode]}, {x.days_before} → {x.days_after} дн.
                    </div>
                  </div>
                  <div className="end">
                    <span className="tiny muted">{date(x.created_at)}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Panel>

        <CustomerExtras customerId={c.id} />

        <div className="wide" style={{ gridColumn: '1 / -1' }}>
          <CustomerGeo id={c.id} />
        </div>

        <Panel icon={Clock} tone="slate" title="История" count={events.length} wide>
          {events.length === 0 ? (
            <Empty icon={Clock}>Событий пока нет</Empty>
          ) : (
            <div className="feed timeline">
              {events.map((e) => (
                <div className="feed-item" key={e.id}>
                  <span className="when num">{dateTime(e.created_at)}</span>
                  <div className="main">
                    <div>
                      {eventName[e.kind] || e.kind} <span className="muted">{eventText(e)}</span>
                    </div>
                  </div>
                  <div className="end">{e.actor_id && <span className="badge">админ</span>}</div>
                </div>
              ))}
            </div>
          )}
        </Panel>
      </div>

      <GrantSheet open={grantOpen} onClose={() => setGrantOpen(false)} plans={plans.data || []} onGrant={(plan_id, days, kind) => subAction(() => post(`/api/admin/customers/${c.id}/subscriptions`, { plan_id, days, kind }), 'Подписка выдана').then(() => setGrantOpen(false))} busy={busy} />
      <DevicesSheet sub={devicesFor} onClose={() => setDevicesFor(null)} />
      <ChangeSheet
        sub={changeFor}
        plans={plans.data || []}
        busy={busy}
        onClose={() => setChangeFor(null)}
        onSave={(plan_id, days) => subAction(() => post(`/api/admin/subscriptions/${changeFor!.id}/change`, { plan_id, days }), 'Тариф изменён').then(() => setChangeFor(null))}
      />
    </div>
  )
}

function SubBlock(p: {
  s: Subscription
  busy: boolean
  onDays: (d: number) => void
  onReset: () => void
  onRevoke: () => void
  onToggle: (en: boolean) => void
  onDelete: () => void
  onDevices: () => void
  onCopy: () => void
  onChange: () => void
  onAutopay: () => void
}) {
  const { s } = p
  const [days, setDays] = useState(7)
  const expired = new Date(s.expires_at) < new Date()
  const disabled = s.status !== 'active'
  const devLimit = s.device_limit ? s.device_limit + s.extra_devices : 0
  const pct = s.traffic_limit_bytes > 0 ? Math.min(100, (s.used_traffic_bytes / s.traffic_limit_bytes) * 100) : 0
  return (
    <div className="sub-item">
      <div className="head">
        <strong>{s.plan_name || s.title}</strong>
        {s.is_primary && <span className="badge accent">основная</span>}
        {s.kind === 'trial' && <span className="badge">пробная</span>}
        {disabled && <span className="badge bad">отключена</span>}
        {s.autopay && <span className="badge ok">автопродление</span>}
        <span className="ids num">
          #{s.id} · rw {s.rw_user_id ?? '—'}
          <button className="copy-btn" aria-label="Скопировать ссылку подписки" title="Скопировать ссылку подписки" onClick={p.onCopy}>
            <Copy />
          </button>
        </span>
      </div>
      <div className="sub-stats">
        <div className="sub-stat">
          <span className="k">Срок</span>
          <span className={expired ? 'v bad' : 'v'}>
            {expired ? 'истекла ' : 'до '}
            {date(s.expires_at)}
          </span>
        </div>
        <div className="sub-stat">
          <span className="k">Трафик</span>
          <span className="v num">
            {bytes(s.used_traffic_bytes)}
            {s.traffic_limit_bytes > 0 ? ` / ${bytes(s.traffic_limit_bytes)}` : ''}
          </span>
          {s.traffic_limit_bytes > 0 && (
            <div className={pct > 90 ? 'meter bad' : pct > 75 ? 'meter warn' : 'meter'}>
              <i style={{ width: `${pct}%` }} />
            </div>
          )}
        </div>
        <div className="sub-stat">
          <span className="k">Устройства</span>
          <span className="v num">
            {s.devices_used}
            {devLimit ? ` / ${devLimit}` : ''}
          </span>
        </div>
      </div>
      <div className="sub-actions-row">
        <div className="days-group">
          <input className="input num" type="number" value={days} onChange={(e) => setDays(Number(e.target.value))} aria-label="Дней" />
          <button className="btn small" disabled={p.busy || !days} onClick={() => p.onDays(days)}>
            {days >= 0 ? 'Добавить дни' : 'Убрать дни'}
          </button>
        </div>
        <button className="btn secondary small" disabled={p.busy} onClick={p.onDevices}>
          Устройства
        </button>
        <button className="btn secondary small" disabled={p.busy} onClick={p.onChange}>
          Сменить тариф
        </button>
        <button className="btn secondary small" disabled={p.busy} onClick={p.onReset}>
          Сбросить трафик
        </button>
        <button className="btn secondary small" disabled={p.busy} onClick={p.onRevoke}>
          Новая ссылка
        </button>
        {s.autopay && (
          <button className="btn secondary small" disabled={p.busy} onClick={p.onAutopay}>
            Запустить автооплату
          </button>
        )}
        <div className="sub-danger">
          <button className="btn danger-ghost small" disabled={p.busy} onClick={() => p.onToggle(disabled)}>
            {disabled ? 'Включить' : 'Отключить'}
          </button>
          <button className="icon-btn" aria-label="Удалить подписку" title="Удалить подписку" onClick={p.onDelete}>
            <Trash />
          </button>
        </div>
      </div>
    </div>
  )
}

function GrantSheet({ open, onClose, plans, onGrant, busy }: { open: boolean; onClose: () => void; plans: Plan[]; onGrant: (plan: number, days: number, kind: string) => void; busy: boolean }) {
  const [plan, setPlan] = useState(0)
  const [days, setDays] = useState(30)
  const [kind, setKind] = useState('free')
  useEffect(() => {
    if (!plan && plans.length) setPlan(plans[0].id)
  }, [plans, plan])
  return (
    <Sheet open={open} onClose={onClose} title="Выдать подписку">
      <div className="stack">
        <Field label="Тариф">
          <select className="input" value={plan} onChange={(e) => setPlan(Number(e.target.value))}>
            {plans.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
                {!p.is_active ? ' (неактивен)' : ''}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Дней">
          <input className="input num" type="number" min={1} value={days} onChange={(e) => setDays(Number(e.target.value))} />
        </Field>
        <Field label="Тип">
          <select className="input" value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="free">Бесплатная</option>
            <option value="trial">Пробная</option>
            <option value="paid">Платная (оплачена вне бота)</option>
          </select>
        </Field>
        <button className="btn" disabled={busy || !plan || days < 1} onClick={() => onGrant(plan, days, kind)}>
          Выдать
        </button>
      </div>
    </Sheet>
  )
}

function DevicesSheet({ sub, onClose }: { sub: Subscription | null; onClose: () => void }) {
  const [list, setList] = useState<Device[] | null>(null)
  const { busy, run } = useAction()
  useEffect(() => {
    if (!sub) return
    let active = true
    setList(null)
    get<Device[]>(`/api/subscriptions/${sub.id}/devices`).then(
      (v) => active && setList(v),
      () => active && setList([]),
    )
    return () => {
      active = false
    }
  }, [sub])
  return (
    <Sheet open={!!sub} onClose={onClose} title="Устройства">
      {!list ? (
        <div className="center" style={{ padding: 20 }}>
          <Spinner />
        </div>
      ) : list.length === 0 ? (
        <div className="empty">Устройств нет</div>
      ) : (
        <div className="list small">
          {list.map((d) => (
            <div className="item" key={d.hwid}>
              <div className="grow">
                <div>
                  {d.deviceModel || 'Устройство'} <span className="badge">{d.platform || '?'}</span>
                </div>
                <div className="tiny muted ellipsis">{d.userAgent}</div>
                <div className="tiny muted">
                  HWID {d.hwid.slice(0, 4)}…{d.hwid.slice(-4)} · {dateTime(d.createdAt)} / {dateTime(d.updatedAt)}
                </div>
              </div>
              <button
                className="icon-btn"
                aria-label="Удалить"
                disabled={busy}
                onClick={() =>
                  run(async () => {
                    await del(`/api/subscriptions/${sub!.id}/devices?hwid=${encodeURIComponent(d.hwid)}`)
                    setList(list.filter((x) => x.hwid !== d.hwid))
                  }, 'Устройство удалено')
                }
              >
                <Trash />
              </button>
            </div>
          ))}
        </div>
      )}
    </Sheet>
  )
}

function ChangeSheet({ sub, plans, busy, onClose, onSave }: { sub: Subscription | null; plans: Plan[]; busy: boolean; onClose: () => void; onSave: (plan: number, days: number | null) => void }) {
  const [plan, setPlan] = useState(0)
  const [keep, setKeep] = useState(true)
  const [days, setDays] = useState(30)
  useEffect(() => {
    if (sub) {
      setPlan(plans.find((p) => p.id !== sub.plan_id)?.id || 0)
      setKeep(true)
    }
  }, [sub, plans])
  return (
    <Sheet open={!!sub} onClose={onClose} title="Сменить тариф">
      <div className="stack">
        <p className="small muted">Сейчас: {sub?.plan_name || sub?.title}, до {date(sub?.expires_at)}. Оплата не создаётся.</p>
        <Field label="Новый тариф">
          <select className="input" value={plan} onChange={(e) => setPlan(Number(e.target.value))}>
            {plans
              .filter((p) => p.id !== sub?.plan_id)
              .map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                  {!p.is_active ? ' (неактивен)' : ''}
                </option>
              ))}
          </select>
        </Field>
        <Toggle checked={keep} onChange={setKeep} label="Сохранить текущий срок" />
        {!keep && (
          <Field label="Новый срок, дней от сегодня">
            <input className="input num" type="number" min={1} value={days} onChange={(e) => setDays(Number(e.target.value))} />
          </Field>
        )}
        <button className="btn" disabled={busy || !plan || (!keep && days < 1)} onClick={() => onSave(plan, keep ? null : days)}>
          Сменить
        </button>
      </div>
    </Sheet>
  )
}
