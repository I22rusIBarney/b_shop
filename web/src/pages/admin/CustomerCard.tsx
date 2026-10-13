import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { del, get, patch, post, type Customer, type Device, type Identity, type Payment, type Plan, type Subscription } from '../../lib/api'
import { bytes, date, dateTime, operationName, paymentStatus, providerName, relative, rub } from '../../lib/format'
import { Back, Copy, Trash } from '../../ui/icons'
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
}

const eventName: Record<string, string> = {
  registered: 'Регистрация',
  login: 'Вход',
  login_linked: 'Привязан способ входа',
  payment_created: 'Создан платёж',
  purchase: 'Покупка',
  trial: 'Пробный период',
  refund: 'Возврат',
  admin_update: 'Изменён администратором',
  admin_days: 'Изменён срок',
  admin_grant: 'Выдана подписка',
  device_deleted: 'Удалено устройство',
  subscription_revoked: 'Перевыпущена ссылка',
  subscription_deleted: 'Удалена подписка',
  subscription_status: 'Статус подписки',
  traffic_reset: 'Сброшен трафик',
  settings_changed: 'Изменены настройки',
}

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
      return `${d.days} дн.`
    case 'login':
    case 'registered':
    case 'login_linked':
      return providerName[d.provider] || d.provider || ''
    default:
      return ''
  }
}

export default function CustomerCard() {
  const { id } = useParams()
  const nav = useNavigate()
  const toast = useToast()
  const data = useLoad(() => get<Detail>(`/api/admin/customers/${id}`), [id])
  const plans = useLoad(() => get<Plan[]>('/api/admin/plans'))
  const { busy, run } = useAction()
  const [notes, setNotes] = useState({ tag: '', notes: '', personal_discount: 0 })
  const [grantOpen, setGrantOpen] = useState(false)
  const [devicesFor, setDevicesFor] = useState<Subscription | null>(null)

  useEffect(() => {
    if (data.data) {
      const c = data.data.customer
      setNotes({ tag: c.tag, notes: c.notes, personal_discount: c.personal_discount })
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
  const name = [c.first_name, c.last_name].filter(Boolean).join(' ') || (c.username ? `@${c.username}` : '') || c.email || `#${c.id}`

  const saveNotes = () =>
    run(async () => {
      const d = await patch<Detail>(`/api/admin/customers/${c.id}`, notes)
      data.setData(d)
    }, 'Сохранено')

  const setFlag = (k: 'is_blocked' | 'is_admin', v: boolean) =>
    run(async () => {
      const d = await patch<Detail>(`/api/admin/customers/${c.id}`, { [k]: v })
      data.setData(d)
    })

  const subAction = (fn: () => Promise<unknown>, ok: string) =>
    run(async () => {
      await fn()
      await data.reload()
    }, ok)

  return (
    <div className="stack lg">
      <div className="admin-head" style={{ marginBottom: 0 }}>
        <div className="row">
          <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
            <Back />
          </button>
          <div>
            <h1>{name}</h1>
            <div className="small muted">
              #{c.id}
              {c.telegram_id ? ` · TG ${c.telegram_id}` : ''}
              {c.username ? ` · @${c.username}` : ''}
              {c.email ? ` · ${c.email}` : ''}
            </div>
          </div>
        </div>
        <div className="row wrap">
          {c.bot_blocked && <span className="badge warn">Заблокировал бота</span>}
          <span className="small muted">Был {relative(c.last_seen_at)}</span>
        </div>
      </div>

      <div className="grid2">
        <section className="card stack">
          <div className="card-title">
            <h3>Подписки</h3>
            <button className="btn secondary small" onClick={() => setGrantOpen(true)}>
              Выдать подписку
            </button>
          </div>
          {subscriptions.length === 0 ? (
            <p className="small muted">Подписок нет</p>
          ) : (
            subscriptions.map((s) => (
              <SubBlock
                key={s.id}
                s={s}
                busy={busy}
                onDays={(days) => subAction(() => post(`/api/admin/subscriptions/${s.id}/days`, { days }), 'Срок изменён')}
                onReset={() => subAction(() => post(`/api/admin/subscriptions/${s.id}/reset-traffic`), 'Трафик сброшен')}
                onRevoke={() => subAction(() => post(`/api/subscriptions/${s.id}/revoke`, { only_passwords: false }), 'Ссылка перевыпущена')}
                onToggle={(en) => subAction(() => post(`/api/admin/subscriptions/${s.id}/status`, { enabled: en }), en ? 'Подписка включена' : 'Подписка отключена')}
                onDelete={() => {
                  if (confirm(`Удалить подписку «${s.plan_name || s.title}» вместе с пользователем в панели?`)) subAction(() => del(`/api/admin/subscriptions/${s.id}`), 'Подписка удалена')
                }}
                onDevices={() => setDevicesFor(s)}
                onCopy={async () => (await copy(s.subscription_url)) && toast('Ссылка скопирована')}
              />
            ))
          )}
        </section>

        <section className="card stack">
          <h3>Заметки администратора</h3>
          <Field label="Тег" hint="Например: VIP, проблемный, тест">
            <input className="input" value={notes.tag} onChange={(e) => setNotes({ ...notes, tag: e.target.value })} />
          </Field>
          <Field label="Персональная скидка, %">
            <input className="input num" type="number" min={0} max={100} value={notes.personal_discount} onChange={(e) => setNotes({ ...notes, personal_discount: Number(e.target.value) })} />
          </Field>
          <Field label="Заметки">
            <textarea className="input" value={notes.notes} onChange={(e) => setNotes({ ...notes, notes: e.target.value })} />
          </Field>
          <div className="row">
            <button className="btn small" disabled={busy} onClick={saveNotes}>
              Сохранить
            </button>
          </div>
          <Toggle checked={c.is_blocked} onChange={(v) => setFlag('is_blocked', v)} label="Заблокировать доступ" hint="Клиент не сможет войти в кабинет и бот" />
          <Toggle checked={c.is_admin} onChange={(v) => setFlag('is_admin', v)} label="Администратор" />
        </section>

        <section className="card stack">
          <h3>Профиль</h3>
          <div className="list small">
            <div className="item">
              <span className="grow muted">Создан</span>
              <span>{dateTime(c.created_at)}</span>
            </div>
            <div className="item">
              <span className="grow muted">Источник</span>
              <span>{c.source || '—'}</span>
            </div>
            <div className="item">
              <span className="grow muted">Пригласил</span>
              {referrer ? <Link to={`/admin/customers/${referrer.id}`}>{referrer.username ? `@${referrer.username}` : `#${referrer.id}`}</Link> : <span>—</span>}
            </div>
            <div className="item">
              <span className="grow muted">Реферальный код</span>
              <span className="num">{c.ref_code}</span>
            </div>
            <div className="item">
              <span className="grow muted">Пробный период</span>
              <span>{c.trial_used ? 'использован' : 'не использован'}</span>
            </div>
            {identities.map((i) => (
              <div className="item" key={i.id}>
                <span className="grow muted">{providerName[i.provider] || i.provider}</span>
                <span className="ellipsis" style={{ maxWidth: 200 }}>
                  {i.display}
                </span>
              </div>
            ))}
          </div>
        </section>

        <section className="card stack">
          <h3>Платежи</h3>
          {payments.length === 0 ? (
            <p className="small muted">Платежей нет</p>
          ) : (
            <div className="list small">
              {payments.slice(0, 15).map((p) => (
                <div className="item" key={p.id}>
                  <div className="grow">
                    <div>
                      {operationName[p.operation] || p.operation}: {p.title}
                    </div>
                    <div className="tiny muted">
                      {dateTime(p.created_at)} · {p.method || p.provider}
                    </div>
                  </div>
                  <div style={{ textAlign: 'right' }}>
                    <div className="num">{rub(p.amount)}</div>
                    <span className={p.status === 'succeeded' ? 'badge ok' : p.status === 'pending' ? 'badge warn' : 'badge'}>{paymentStatus[p.status]}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="card stack" style={{ gridColumn: '1 / -1' }}>
          <h3>История</h3>
          <div className="list small">
            {events.map((e) => (
              <div className="item" key={e.id}>
                <span className="muted num" style={{ width: 110, flex: 'none' }}>
                  {dateTime(e.created_at)}
                </span>
                <span className="grow">
                  {eventName[e.kind] || e.kind} <span className="muted">{eventText(e)}</span>
                </span>
                {e.actor_id && <span className="badge">админ</span>}
              </div>
            ))}
          </div>
        </section>
      </div>

      <GrantSheet open={grantOpen} onClose={() => setGrantOpen(false)} plans={plans.data || []} onGrant={(plan_id, days, kind) => subAction(() => post(`/api/admin/customers/${c.id}/subscriptions`, { plan_id, days, kind }), 'Подписка выдана').then(() => setGrantOpen(false))} busy={busy} />
      <DevicesSheet sub={devicesFor} onClose={() => setDevicesFor(null)} />
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
}) {
  const { s } = p
  const [days, setDays] = useState(7)
  const expired = new Date(s.expires_at) < new Date()
  const disabled = s.status !== 'active'
  return (
    <div className="card flat stack">
      <div className="spread">
        <div>
          <strong>{s.plan_name || s.title}</strong>
          {s.is_primary && <span className="badge accent" style={{ marginLeft: 8 }}>основная</span>}
          {s.kind === 'trial' && <span className="badge" style={{ marginLeft: 6 }}>пробная</span>}
          {disabled && <span className="badge bad" style={{ marginLeft: 6 }}>отключена</span>}
        </div>
        <span className="tiny muted num">
          #{s.id} · rw:{s.rw_user_id ?? '—'}
        </span>
      </div>
      <div className="small">
        <span style={{ color: expired ? 'var(--danger)' : undefined }}>до {date(s.expires_at)}</span>
        <span className="muted">
          {' '}
          · трафик {bytes(s.used_traffic_bytes)}
          {s.traffic_limit_bytes > 0 ? ` из ${bytes(s.traffic_limit_bytes)}` : ''} · устройства {s.devices_used}
          {s.device_limit ? `/${s.device_limit + s.extra_devices}` : ''}
        </span>
      </div>
      <div className="row wrap">
        <input className="input num" type="number" value={days} onChange={(e) => setDays(Number(e.target.value))} style={{ width: 90, minHeight: 36 }} aria-label="Дней" />
        <button className="btn small" disabled={p.busy || !days} onClick={() => p.onDays(days)}>
          {days >= 0 ? 'Добавить дни' : 'Убрать дни'}
        </button>
        <button className="btn secondary small" disabled={p.busy} onClick={p.onDevices}>
          Устройства
        </button>
        <button className="btn secondary small" disabled={p.busy} onClick={p.onReset}>
          Сбросить трафик
        </button>
        <button className="btn secondary small" disabled={p.busy} onClick={p.onRevoke}>
          Новая ссылка
        </button>
        <button className="btn secondary small" disabled={p.busy} onClick={() => p.onToggle(disabled)}>
          {disabled ? 'Включить' : 'Отключить'}
        </button>
        <button className="icon-btn" aria-label="Скопировать ссылку" onClick={p.onCopy}>
          <Copy />
        </button>
        <button className="icon-btn" aria-label="Удалить подписку" onClick={p.onDelete}>
          <Trash />
        </button>
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
    setList(null)
    get<Device[]>(`/api/subscriptions/${sub.id}/devices`).then(setList, () => setList([]))
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
