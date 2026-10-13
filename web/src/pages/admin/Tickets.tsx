import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { get, patch, post, type AdminTicketView, type SupportTemplate, type TicketMessage, type TicketRow } from '../../lib/api'
import { refreshBadges } from '../../lib/badges'
import { date, relative } from '../../lib/format'
import { Back } from '../../ui/icons'
import { Composer, Messages, ticketStatus } from '../../ui/chat'
import { ErrorBox, Spinner, Toggle, useAction, useLoad, usePolling, pressable } from '../../ui/kit'

const tabs = [
  ['active', 'Активные'],
  ['open', 'Ждут ответа'],
  ['waiting', 'Ждут клиента'],
  ['resolved', 'Решённые'],
  ['closed', 'Закрытые'],
  ['all', 'Все'],
] as const

const authorLabel: Record<string, string> = { customer: 'клиент', operator: 'вы', ai: 'ИИ' }

export function TicketList() {
  const nav = useNavigate()
  const [status, setStatus] = useState('active')
  const [q, setQ] = useState('')
  const [offset, setOffset] = useState(0)
  const data = useLoad(() => get<{ items: TicketRow[]; total: number }>(`/api/admin/tickets?status=${status}&q=${encodeURIComponent(q)}&offset=${offset}&limit=50`), [status, q, offset])

  usePolling(data.refresh, 15000)

  return (
    <>
      <div className="admin-head">
        <h1>Обращения</h1>
        <Link className="btn secondary small" to="/admin/support/knowledge">
          Шаблоны и база знаний
        </Link>
      </div>
      <div className="chips">
        {tabs.map(([id, label]) => (
          <button key={id} className={status === id ? 'chip on' : 'chip'} onClick={() => (setStatus(id), setOffset(0))}>
            {label}
          </button>
        ))}
      </div>
      <input className="input" placeholder="Поиск: номер, тема, клиент, Telegram ID" value={q} onChange={(e) => (setQ(e.target.value), setOffset(0))} style={{ maxWidth: 360, marginBottom: 12 }} />
      {data.error ? (
        <ErrorBox text={data.error} retry={data.reload} />
      ) : !data.data ? (
        <div className="center" style={{ padding: 40 }}>
          <Spinner />
        </div>
      ) : data.data.items.length === 0 ? (
        <div className="card empty">
          <h3>Обращений нет</h3>
        </div>
      ) : (
        <>
          <div className="ticket-cards">
            {data.data.items.map((t) => {
              const [label, cls] = ticketStatus[t.status]
              return (
                <button key={t.id} className={t.unread_admin ? 'ticket-card unread' : 'ticket-card'} onClick={() => nav(`/admin/tickets/${t.id}`)}>
                  <div className="top">
                    <span className="muted num">#{t.id}</span>
                    <b>{t.subject}</b>
                    <span className={cls}>{label}</span>
                  </div>
                  {t.last_body && (
                    <div className="last">
                      {t.last_author && `${authorLabel[t.last_author] || t.last_author}: `}
                      {t.last_body}
                    </div>
                  )}
                  <div className="meta">
                    <span>{t.customer_name}{t.username ? ` · @${t.username}` : t.telegram_id ? ` · ${t.telegram_id}` : ''}</span>
                    <span>· {relative(t.updated_at)}</span>
                    {t.pending_draft && <span className="badge warn">черновик ИИ</span>}
                    {t.ai_enabled && t.status !== 'closed' && <span className="badge">ИИ</span>}
                    {t.rating ? <span>· {'★'.repeat(t.rating)}</span> : null}
                  </div>
                </button>
              )
            })}
          </div>
          <div className="table-wrap ticket-table">
            <table className="t">
              <thead>
                <tr>
                  <th>№</th>
                  <th>Тема и последнее сообщение</th>
                  <th>Клиент</th>
                  <th>Статус</th>
                  <th>Обновлено</th>
                  <th>Оценка</th>
                </tr>
              </thead>
              <tbody>
                {data.data.items.map((t) => {
                  const [label, cls] = ticketStatus[t.status]
                  return (
                    <tr key={t.id} className="click" {...pressable(() => nav(`/admin/tickets/${t.id}`))} style={t.unread_admin ? { fontWeight: 700 } : undefined}>
                      <td className="num">#{t.id}</td>
                      <td style={{ maxWidth: 380 }}>
                        <div className="ellipsis">{t.subject}</div>
                        <div className="tiny muted ellipsis" style={{ fontWeight: 400 }}>
                          {t.last_author && `${authorLabel[t.last_author] || t.last_author}: `}
                          {t.last_body}
                        </div>
                      </td>
                      <td>
                        <div>{t.customer_name}</div>
                        <div className="tiny muted">{t.username ? '@' + t.username : t.telegram_id}</div>
                      </td>
                      <td>
                        <div className="row" style={{ gap: 6 }}>
                          <span className={cls}>{label}</span>
                          {t.pending_draft && <span className="badge warn">черновик ИИ</span>}
                          {t.ai_enabled && t.status !== 'closed' && <span className="badge">ИИ</span>}
                        </div>
                      </td>
                      <td className="muted">{relative(t.updated_at)}</td>
                      <td>{t.rating ? '★'.repeat(t.rating) : <span className="muted">—</span>}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          <div className="pager">
            <span className="small muted num">
              {offset + 1}–{offset + data.data.items.length} из {data.data.total}
            </span>
            <button className="btn secondary small" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>
              Назад
            </button>
            <button className="btn secondary small" disabled={offset + 50 >= data.data.total} onClick={() => setOffset(offset + 50)}>
              Дальше
            </button>
          </div>
        </>
      )}
    </>
  )
}

export function TicketChat() {
  const { id } = useParams()
  const nav = useNavigate()
  const t = useLoad(() => get<AdminTicketView>(`/api/admin/tickets/${id}`), [id])
  const templates = useLoad(() => get<SupportTemplate[]>('/api/admin/support/templates'))
  const { busy, run } = useAction()
  const [text, setText] = useState('')
  const [note, setNote] = useState(false)
  const [after, setAfter] = useState('waiting')
  const [editDraft, setEditDraft] = useState<Record<number, string>>({})

  useEffect(() => {
    refreshBadges()
  }, [id])
  // New messages from the client and AI drafts show up without a reload; the reply box keeps its text.
  usePolling(t.refresh, 8000)

  if (t.error && !t.data) return <ErrorBox text={t.error} retry={t.reload} />
  if (!t.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )
  const v = t.data
  const c = v.customer
  const [label, cls] = ticketStatus[v.status]

  const setField = (p: Record<string, unknown>, ok?: string) =>
    run(async () => {
      await patch(`/api/admin/tickets/${v.id}`, p)
      await t.reload()
    }, ok)

  const draftAction = (m: TicketMessage, action: 'send' | 'reject') =>
    run(async () => {
      await post(`/api/admin/ticket-messages/${m.id}/draft`, { action, body: editDraft[m.id] ?? m.body })
      await t.reload()
    }, action === 'send' ? 'Отправлено' : 'Отклонено')

  const suggest = () =>
    run(async () => {
      const a = await post<{ reply: string; confidence: number; escalate: boolean; reason: string }>(`/api/admin/tickets/${v.id}/suggest`)
      setText(a.reply || (a.escalate ? `(ассистент считает, что нужен оператор: ${a.reason})` : ''))
    })

  return (
    <div className="stack lg">
      <div className="row" style={{ alignItems: 'flex-start' }}>
        <button className="icon-btn" style={{ marginTop: 2, flex: 'none' }} onClick={() => nav('/admin/tickets')} aria-label="К списку">
          <Back />
        </button>
        <div className="grow" style={{ minWidth: 0 }}>
          <h1 className="ellipsis">
            #{v.id} {v.subject}
          </h1>
          <div className="row wrap" style={{ gap: 8 }}>
            <span className={cls}>{label}</span>
            <span className="tiny muted">создано {date(v.created_at)} · {v.source === 'telegram' ? 'из бота' : 'из кабинета'}</span>
            {v.rating && <span className="badge">★ {v.rating}{v.rating_comment && ` · ${v.rating_comment}`}</span>}
          </div>
        </div>
      </div>

      <div className="ticket-layout">
        <div className="stack">
          <div className="card">
            <Messages
              messages={v.messages}
              viewer="admin"
              renderExtra={(m) =>
                m.author === 'draft' &&
                m.draft_status === 'pending' && (
                  <div className="stack" style={{ marginTop: 8 }}>
                    <textarea className="input" value={editDraft[m.id] ?? m.body} onChange={(e) => setEditDraft({ ...editDraft, [m.id]: e.target.value })} />
                    <div className="row">
                      <button className="btn small" disabled={busy} onClick={() => draftAction(m, 'send')}>
                        Отправить
                      </button>
                      <button className="btn ghost small" disabled={busy} onClick={() => draftAction(m, 'reject')}>
                        Отклонить
                      </button>
                    </div>
                  </div>
                )
              }
            />
          </div>
          <div className="card stack">
            <Composer
              admin
              templates={templates.data}
              value={text}
              onChange={setText}
              placeholder={note ? 'Внутренняя заметка (клиент не увидит)' : 'Ответ клиенту'}
              onSend={async (body, attachments) => {
                await post(`/api/admin/tickets/${v.id}/messages`, { body, attachments, note, status: after })
                await t.reload()
              }}
              extra={
                <>
                  <button className="btn secondary small" disabled={busy} onClick={suggest}>
                    Подсказка ИИ
                  </button>
                  {!note && (
                    <select className="input" style={{ width: 'auto', minHeight: 36 }} value={after} onChange={(e) => setAfter(e.target.value)} aria-label="Статус после ответа">
                      <option value="waiting">→ ждём клиента</option>
                      <option value="resolved">→ решено</option>
                      <option value="open">→ оставить открытым</option>
                    </select>
                  )}
                </>
              }
            />
            <Toggle checked={note} onChange={setNote} label="Внутренняя заметка" />
          </div>
        </div>

        <section className="card ticket-info ticket-side">
          <div>
            <Link to={`/admin/customers/${c.id}`}>
              <b>{[c.first_name, c.last_name].filter(Boolean).join(' ') || c.username || c.email || `#${c.id}`}</b>
            </Link>
            <div className="tiny muted">{[c.telegram_id, c.username && '@' + c.username, c.email].filter(Boolean).join(' · ')}</div>
          </div>
          {v.subscriptions.length === 0 && <p className="small muted">Подписок нет</p>}
          {v.subscriptions.map((s) => (
            <div key={s.id} className="small">
              <b>{s.title}</b> <span className="badge">{s.kind === 'trial' ? 'пробная' : s.kind === 'free' ? 'бесплатная' : 'платная'}</span>
              <div className="muted tiny">
                до {date(s.expires_at)} · устройств {s.devices_used}/{s.device_limit ? s.device_limit + s.extra_devices : '∞'}
              </div>
            </div>
          ))}
          <div className="ctl">
            <select className="input" value={v.status} onChange={(e) => setField({ status: e.target.value }, 'Статус изменён')} aria-label="Статус">
              {Object.entries(ticketStatus).map(([k, [l]]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
            <input className="input" placeholder="Тег" defaultValue={v.tag} onBlur={(e) => e.target.value !== v.tag && setField({ tag: e.target.value })} />
          </div>
          <Toggle checked={v.ai_enabled} onChange={(on) => setField({ ai_enabled: on }, on ? 'Ассистент включён' : 'Ассистент выключен')} label="Ассистент отвечает" hint="Выключается сам, когда вы отвечаете" />
          <Link className="btn secondary small" to={`/admin/customers/${c.id}`}>
            Карточка: дни, триал, трафик
          </Link>
        </section>
      </div>
    </div>
  )
}
