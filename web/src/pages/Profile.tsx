import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { openExternal, useClientMenu } from '../ui/menu'
import { del, get, post, put, type AutopayInfo, type Paged, type Payment, type PaymentMethodInfo, type RedeemResult, type Redemption, type Session, type Subscription } from '../lib/api'
import { useApp } from '../lib/app'
import { date, dateTime, operationName, paymentStatus, plural, providerName, rub } from '../lib/format'
import { haptic } from '../lib/tg'
import { Card, Chevron, Tag, Trash } from '../ui/icons'
import { Field, Sheet, Toggle, useAction, useLoad } from '../ui/kit'

// iOS Settings-style coloured tiles, one hue per kind of action.
const tone: Record<string, string> = {
  news: 'orange', locations: 'blue', buy: 'green', promo: 'pink', buyfor: 'indigo', gifts: 'red', ref: 'purple',
  support: 'green', faq: 'teal', logins: 'gray', payments: 'blue', cards: 'green', exit: 'red',
}

/** Phone "Профиль" tab (reference 3555–3556): the cabinet sections as grouped row cards. */
export default function Profile() {
  const { me } = useApp()
  const { groups, sheets } = useClientMenu()
  if (!me) return null
  const c = me.customer
  const name = [c.first_name, c.last_name].filter(Boolean).join(' ') || (c.username ? `@${c.username}` : c.email) || 'Профиль'

  return (
    <div className="menu-page">
      <header className="large-title">
        <h1>{name}</h1>
      </header>
      {c.personal_discount > 0 && (
        <div className="callout">
          <span className="callout-ico">
            <Tag />
          </span>
          <div>
            <b>Ваша скидка {c.personal_discount}%</b>
            <p>Применяется ко всем покупкам автоматически.</p>
          </div>
        </div>
      )}
      {groups.map((g) => {
        const items = g.items.filter((it) => it.only !== 'desktop')
        if (!items.length) return null
        return (
          <section key={g.title} className="group">
            <h3 className="group-title">{g.title}</h3>
            <div className="group-card">
              {items.map((it) => {
                const inner = (
                  <>
                    <span className={`set-ico ${tone[it.key] || 'gray'}`} aria-hidden="true">
                      <it.icon />
                    </span>
                    <span className="grow">{it.label}</span>
                    {!!it.badge && <span className="am-badge">{it.badge}</span>}
                    <Chevron className="chev" />
                  </>
                )
                const cls = it.key === 'exit' ? 'group-row nav-row danger' : 'group-row nav-row'
                return it.to ? (
                  <Link key={it.key} className={cls} to={it.to}>
                    {inner}
                  </Link>
                ) : it.href ? (
                  <a key={it.key} className={cls} href={it.href} target="_blank" rel="noopener noreferrer" onClick={(e) => openExternal(e, it.href!)}>
                    {inner}
                  </a>
                ) : (
                  <button key={it.key} type="button" className={cls} onClick={it.onClick}>
                    {inner}
                  </button>
                )
              })}
            </div>
          </section>
        )
      })}
      {sheets}
    </div>
  )
}

export function PaymentsSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const data = useLoad(() => (open ? get<Paged<Payment>>('/api/payments') : Promise.resolve(undefined)), [open])
  return (
    <Sheet open={open} onClose={onClose} title="Платежи">
      {!data.data ? (
        <div className="center" style={{ padding: 20 }}>
          <span className="spinner" />
        </div>
      ) : data.data.items.length === 0 ? (
        <div className="empty">Платежей пока не было</div>
      ) : (
        <div className="list">
          {data.data.items.map((p) => (
            <div className="item" key={p.id}>
              <div className="grow">
                <div>
                  {operationName[p.operation] || p.operation}: {p.title}
                </div>
                <div className="tiny muted">{dateTime(p.created_at)}</div>
              </div>
              <div style={{ textAlign: 'right' }}>
                <div className="num">{rub(p.amount)}</div>
                <span className={p.status === 'succeeded' ? 'badge ok' : p.status === 'pending' ? 'badge warn' : 'badge'}>{paymentStatus[p.status]}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </Sheet>
  )
}

export function LoginsSheet({ open, onClose, onChanged }: { open: boolean; onClose: () => void; onChanged: () => Promise<void> }) {
  const { me, config } = useApp()
  const { busy, run } = useAction()
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [sent, setSent] = useState(false)
  if (!me) return null
  const has = new Set(me.identities.map((i) => i.provider))

  const link = (provider: string) =>
    run(async () => {
      const r = await post<{ url: string }>(`/api/auth/oauth/${provider}/start`, { return_to: '/profile' })
      window.location.href = r.url
    })

  return (
    <Sheet open={open} onClose={onClose} title="Способы входа">
      <p className="small muted" style={{ marginBottom: 12 }}>
        Привяжите несколько способов — тогда в кабинет можно будет зайти и без Telegram.
      </p>
      <div className="list">
        {me.identities.map((i) => (
          <div className="item" key={i.id}>
            <div className="grow">
              <div>{providerName[i.provider] || i.provider}</div>
              <div className="tiny muted ellipsis">{i.display}</div>
            </div>
            {me.identities.length > 1 && (
              <button
                className="icon-btn"
                aria-label="Отвязать"
                disabled={busy}
                onClick={() =>
                  run(async () => {
                    await del(`/api/me/identities/${i.id}`)
                    await onChanged()
                  }, 'Способ входа отвязан')
                }
              >
                <Trash />
              </button>
            )}
          </div>
        ))}
      </div>
      <div className="stack" style={{ marginTop: 16 }}>
        {config.auth.email && !has.has('email') && (
          <form
            className="stack"
            onSubmit={(e) => {
              e.preventDefault()
              if (!sent) run(async () => (await post('/api/auth/email/send', { email }), setSent(true)))
              else
                run(async () => {
                  await post<Session>('/api/auth/email/verify', { email, code })
                  await onChanged()
                  setSent(false)
                  setCode('')
                }, 'Почта привязана')
            }}
          >
            <Field label="Привязать почту">
              <div className="input-row">
                <input className="input" type="email" placeholder="you@example.com" value={email} onChange={(e) => setEmail(e.target.value)} disabled={sent} />
                {sent && <input className="input" inputMode="numeric" placeholder="Код" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} style={{ maxWidth: 110 }} />}
              </div>
            </Field>
            <button className="btn secondary" disabled={busy || !email || (sent && code.length !== 6)}>
              {sent ? 'Подтвердить' : 'Получить код'}
            </button>
          </form>
        )}
        {config.auth.yandex && !has.has('yandex') && (
          <button className="btn secondary" disabled={busy} onClick={() => link('yandex')}>
            Привязать Яндекс ID
          </button>
        )}
        {config.auth.vk && !has.has('vk') && (
          <button className="btn secondary" disabled={busy} onClick={() => link('vk')}>
            Привязать VK ID
          </button>
        )}
      </div>
    </Sheet>
  )
}

export function AutopaySheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const subs = useLoad(() => (open ? get<Subscription[]>('/api/subscriptions?live=0') : Promise.resolve(undefined)), [open])
  const methods = useLoad(() => (open ? get<PaymentMethodInfo[]>('/api/payment-methods') : Promise.resolve(undefined)), [open])
  const { busy, run } = useAction()
  const paid = (subs.data || []).filter((s) => s.kind === 'paid')
  return (
    <Sheet open={open} onClose={onClose} title="Автопродление">
      {!subs.data || !methods.data ? (
        <div className="center" style={{ padding: 20 }}>
          <span className="spinner" />
        </div>
      ) : (
        <div className="stack lg">
          {paid.length === 0 ? (
            <div className="empty">Автопродление доступно для платных подписок</div>
          ) : (
            <div className="list">
              {paid.map((s, i) => (
                <AutopayRow key={s.id} sub={s} first={i === 0} hasMethods={methods.data!.length > 0} onChanged={subs.reload} />
              ))}
            </div>
          )}
          <div className="stack">
            <h3>Сохранённые способы оплаты</h3>
            {methods.data.length === 0 ? (
              <p className="small muted">Пока нет. Включите «Автопродление» при следующей оплате — способ оплаты сохранится.</p>
            ) : (
              <div className="list">
                {methods.data.map((m) => (
                  <div className="item" key={m.id}>
                    <Card style={{ width: 20, height: 20 }} />
                    <div className="grow">
                      <div>{m.title || 'Способ оплаты'}</div>
                      <div className="tiny muted">сохранён {date(m.created_at)}</div>
                    </div>
                    <button
                      className="icon-btn"
                      aria-label="Удалить способ оплаты"
                      disabled={busy}
                      onClick={() =>
                        run(async () => {
                          await del(`/api/payment-methods/${m.id}`)
                          await Promise.all([methods.reload(), subs.reload()])
                        }, 'Способ оплаты удалён')
                      }
                    >
                      <Trash />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </Sheet>
  )
}

function AutopayRow({ sub, first, hasMethods, onChanged }: { sub: Subscription; first: boolean; hasMethods: boolean; onChanged: () => void }) {
  const info = useLoad(() => get<AutopayInfo>(`/api/subscriptions/${sub.id}/autopay`), [sub.id, sub.autopay])
  const { busy, run } = useAction()
  const a = info.data
  const set = (on: boolean) =>
    run(async () => {
      await put(`/api/subscriptions/${sub.id}/autopay`, { enabled: on })
      onChanged()
      info.reload()
    }, on ? 'Автопродление включено' : 'Автопродление отключено')
  return (
    <div className="item" style={{ borderTop: first ? 0 : '1px solid var(--line)', alignItems: 'flex-start' }}>
      <div className="grow">
        <div>{sub.plan_name || sub.title}</div>
        <div className="tiny muted">
          {!a
            ? '…'
            : !a.available
              ? a.reason
              : a.enabled
                ? `Списание ${date(a.charge_at)}: ${rub(a.amount)} за ${a.days} ${plural(a.days, "день", "дня", "дней")}`
                : hasMethods
                  ? `Выключено · ${rub(a.amount)} за ${a.days} ${plural(a.days, 'день', 'дня', 'дней')}`
                  : 'Нет сохранённого способа оплаты'}
        </div>
      </div>
      <Toggle checked={!!a?.enabled} onChange={set} label="" disabled={busy || !a || (!a.enabled && (!a.available || !hasMethods))} />
    </div>
  )
}

export function PromoSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const active = useLoad(() => (open ? get<{ active: Redemption[] }>('/api/promo') : Promise.resolve(undefined)), [open])
  const [code, setCode] = useState('')
  const [done, setDone] = useState('')
  const [unlocked, setUnlocked] = useState(false)
  const nav = useNavigate()
  const { busy, run } = useAction()
  const apply = () =>
    run(async () => {
      const r = await post<RedeemResult>('/api/promo', { code })
      haptic('success')
      setDone(r.message)
      setUnlocked(r.kind === 'unlock')
      setCode('')
      active.reload()
    })
  return (
    <Sheet open={open} onClose={onClose} title="Промокод" icon={<Tag />}>
      <form
        className="stack"
        onSubmit={(e) => {
          e.preventDefault()
          apply()
        }}
      >
        <div className="input-row">
          <input className="input" placeholder="Введите промокод" value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} autoCapitalize="characters" />
          <button className="btn" disabled={busy || !code.trim()}>
            Применить
          </button>
        </div>
        {done && <p className="ok-text">✓ {done}</p>}
        {unlocked && (
          <button type="button" className="btn" onClick={() => nav('/buy')}>
            Посмотреть тариф
          </button>
        )}
      </form>
      {active.data && active.data.active.length > 0 && (
        <div className="stack" style={{ marginTop: 18 }}>
          <h3>Ждут оплаты</h3>
          <div className="list">
            {active.data.active.map((r) => (
              <div className="item" key={r.id}>
                <span className="badge accent">−{r.value}%</span>
                <span className="grow">{r.code}</span>
                <span className="tiny muted">к следующей оплате</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </Sheet>
  )
}
