import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { del, get, post, type Paged, type Payment, type Session } from '../lib/api'
import { useApp } from '../lib/app'
import { dateTime, operationName, paymentStatus, providerName, rub } from '../lib/format'
import { haptic, openLink } from '../lib/tg'
import { Card, Chevron, Copy, Exit, Link as LinkIcon, Trash, Users, Wallet } from '../ui/icons'
import { Field, Sheet, copy, useAction, useLoad, useToast } from '../ui/kit'

export default function Profile() {
  const { me, config, signOut, refreshMe } = useApp()
  const nav = useNavigate()
  const toast = useToast()
  const [sheet, setSheet] = useState<'' | 'payments' | 'logins' | 'cards'>('')
  if (!me) return null
  const c = me.customer
  const name = [c.first_name, c.last_name].filter(Boolean).join(' ') || (c.username ? `@${c.username}` : c.email) || 'Профиль'

  return (
    <div className="stack lg">
      <div className="card">
        <h2>{name}</h2>
        <p className="small muted" style={{ marginTop: 4 }}>
          {[c.username && `@${c.username}`, c.email].filter(Boolean).join(', ') || `ID ${c.id}`}
        </p>
        {c.personal_discount > 0 && (
          <span className="badge accent" style={{ marginTop: 10 }}>
            Ваша скидка {c.personal_discount}%
          </span>
        )}
      </div>

      {me.ref_link && (
        <div className="card stack">
          <div className="row">
            <Users style={{ width: 22, height: 22, color: 'var(--accent)' }} />
            <h3 className="grow">Пригласить друзей</h3>
          </div>
          <p className="small muted">Друг получает бонусные дни при первой оплате, вы — тоже. Ссылка:</p>
          <div className="row">
            <div className="mono-box grow ellipsis">{me.ref_link.replace('https://', '')}</div>
            <button
              className="icon-btn"
              aria-label="Скопировать ссылку"
              onClick={async () => {
                if (await copy(me.ref_link)) {
                  haptic('success')
                  toast('Ссылка скопирована')
                }
              }}
            >
              <Copy />
            </button>
          </div>
        </div>
      )}

      <div className="card list">
        <button className="item item-link" style={{ background: 'none', border: 0, width: '100%', textAlign: 'left' }} onClick={() => setSheet('payments')}>
          <Wallet style={{ width: 20, height: 20 }} />
          <span>История платежей</span>
          <Chevron className="chev" style={{ width: 18, height: 18 }} />
        </button>
        <button className="item item-link" style={{ background: 'none', border: 0, borderTop: '1px solid var(--line)', width: '100%', textAlign: 'left' }} onClick={() => setSheet('logins')}>
          <LinkIcon style={{ width: 20, height: 20 }} />
          <span>Способы входа</span>
          <Chevron className="chev" style={{ width: 18, height: 18 }} />
        </button>
        <button className="item item-link" style={{ background: 'none', border: 0, borderTop: '1px solid var(--line)', width: '100%', textAlign: 'left' }} onClick={() => setSheet('cards')}>
          <Card style={{ width: 20, height: 20 }} />
          <span>Сохранённые карты</span>
          <Chevron className="chev" style={{ width: 18, height: 18 }} />
        </button>
      </div>

      {config.links.length > 0 && (
        <div className="card list">
          {config.links.map((l, i) => (
            <button key={i} className="item item-link" style={{ background: 'none', border: 0, borderTop: i ? '1px solid var(--line)' : 0, width: '100%', textAlign: 'left' }} onClick={() => openLink(l.url)}>
              <span>{l.title}</span>
              <Chevron className="chev" style={{ width: 18, height: 18 }} />
            </button>
          ))}
        </div>
      )}

      <button
        className="btn ghost block"
        onClick={async () => {
          await signOut()
          nav('/login')
        }}
      >
        <Exit /> Выйти
      </button>

      <PaymentsSheet open={sheet === 'payments'} onClose={() => setSheet('')} />
      <LoginsSheet open={sheet === 'logins'} onClose={() => setSheet('')} onChanged={refreshMe} />
      <CardsSheet open={sheet === 'cards'} onClose={() => setSheet('')} />
    </div>
  )
}

function PaymentsSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
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

function LoginsSheet({ open, onClose, onChanged }: { open: boolean; onClose: () => void; onChanged: () => Promise<void> }) {
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

function CardsSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const data = useLoad(() => (open ? get<{ id: number; title: string; created_at: string }[]>('/api/payment-methods') : Promise.resolve(undefined)), [open])
  const { busy, run } = useAction()
  return (
    <Sheet open={open} onClose={onClose} title="Сохранённые карты">
      {!data.data ? (
        <div className="center" style={{ padding: 20 }}>
          <span className="spinner" />
        </div>
      ) : data.data.length === 0 ? (
        <div className="empty">Сохранённых карт нет. Карту можно запомнить при оплате.</div>
      ) : (
        <div className="list">
          {data.data.map((m) => (
            <div className="item" key={m.id}>
              <Card style={{ width: 20, height: 20 }} />
              <span className="grow">{m.title || 'Карта'}</span>
              <button
                className="icon-btn"
                aria-label="Удалить карту"
                disabled={busy}
                onClick={() =>
                  run(async () => {
                    await del(`/api/payment-methods/${m.id}`)
                    data.reload()
                  }, 'Карта удалена')
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
