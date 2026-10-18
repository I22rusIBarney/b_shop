import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { get, patch, post, type AdminTicketView, type SupportTemplate, type TicketMessage, type TicketRow } from '../../lib/api'
import { refreshBadges } from '../../lib/badges'
import { date, relative } from '../../lib/format'
import { Avatar, Empty, InfoRow, Panel } from '../../ui/admin'
import { Back, Bolt, Calendar, Chat, Send, Sliders, User } from '../../ui/icons'
import { Composer, Messages, ticketStatus } from '../../ui/chat'
import { ErrorBox, Spinner, Toggle, useAction, useLoad, usePolling } from '../../ui/kit'

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
      <input
        className="input"
        type="search"
        placeholder="Поиск: номер, тема, клиент, Telegram ID"
        value={q}
        onChange={(e) => (setQ(e.target.value), setOffset(0))}
        style={{ maxWidth: 420, marginBottom: 12 }}
        aria-label="Поиск обращений"
      />
      {data.error ? (
        <ErrorBox text={data.error} retry={data.reload} />
      ) : !data.data ? (
        <div className="center" style={{ padding: 40 }}>
          <Spinner />
        </div>
      ) : data.data.items.length === 0 ? (
        <div className="panel">
          <div className="panel-body">
            <Empty icon={Chat}>{q ? 'Ничего не нашлось — проверьте номер, имя или Telegram ID' : 'Обращений в этом разделе нет'}</Empty>
          </div>
        </div>
      ) : (
        <>
          <div className="tk-list">
            {data.data.items.map((t) => {
              const [label] = ticketStatus[t.status]
              const who = t.customer_name || (t.username ? '@' + t.username : String(t.telegram_id || ''))
              return (
                <button key={t.id} className={t.unread_admin ? 'tk-row unread' : 'tk-row'} onClick={() => nav(`/admin/tickets/${t.id}`)}>
                  <Avatar name={who} seed={t.customer_id} size={40} />
                  <div className="tk-main">
                    <div className="tk-title">
                      <span className="id num">#{t.id}</span>
                      <b>{t.subject}</b>
                      {t.unread_admin && <span className="tk-new">новое</span>}
                    </div>
                    {t.last_body && (
                      <div className="tk-last">
                        {t.last_author && <em>{authorLabel[t.last_author] || t.last_author}: </em>}
                        {t.last_body}
                      </div>
                    )}
                    <div className="tk-who">
                      {who}
                      {t.username && t.customer_name ? ` · @${t.username}` : ''}
                    </div>
                  </div>
                  <div className="tk-meta">
                    {t.pending_draft && <span className="tk-flag draft">черновик ИИ</span>}
                    {t.ai_enabled && t.status !== 'closed' && <span className="tk-flag">ИИ</span>}
                    {t.rating ? <span className="tk-flag">★ {t.rating}</span> : null}
                    <span className={`status ${t.status}`}>{label}</span>
                    <span className="when">{relative(t.updated_at)}</span>
                  </div>
                </button>
              )
            })}
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
  const [label] = ticketStatus[v.status]

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

  const name = [c.first_name, c.last_name].filter(Boolean).join(' ') || (c.username ? '@' + c.username : '') || c.email || `#${c.id}`
  const handle = [c.username && '@' + c.username, c.email].filter(Boolean).join(' · ')

  return (
    <div className="stack lg">
      <div className="tk-head">
        <button className="icon-btn" style={{ flex: 'none' }} onClick={() => nav('/admin/tickets')} aria-label="К списку">
          <Back />
        </button>
        <div className="grow" style={{ minWidth: 0 }}>
          <h1 className="ellipsis">
            <span className="muted num">#{v.id}</span> {v.subject}
          </h1>
          <div className="sub">
            <span className={`status ${v.status}`}>{label}</span>
            <span>
              {date(v.created_at)} · {v.source === 'telegram' || v.source === 'bot' ? 'из бота' : 'из кабинета'}
            </span>
            {v.rating && (
              <span className="tk-flag">
                ★ {v.rating}
                {v.rating_comment && ` · ${v.rating_comment}`}
              </span>
            )}
          </div>
        </div>
      </div>

      <div className="tk-layout">
        <div className="tk-thread">
          <div className="tk-scroll">
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
          <div className={note ? 'tk-reply note' : 'tk-reply'}>
            <Composer
              admin
              templates={templates.data}
              value={text}
              onChange={setText}
              placeholder={note ? 'Внутренняя заметка — клиент её не увидит' : 'Ответ клиенту'}
              onSend={async (body, attachments) => {
                await post(`/api/admin/tickets/${v.id}/messages`, { body, attachments, note, status: after })
                await t.reload()
              }}
              extra={
                <>
                  <button className="btn secondary small" disabled={busy} onClick={suggest}>
                    <Bolt /> Подсказка ИИ
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
            <div className="tk-reply-foot">
              <Toggle checked={note} onChange={setNote} label="Внутренняя заметка" />
            </div>
          </div>
        </div>

        <aside className="tk-side">
          <Panel icon={User} tone="blue" title="Клиент">
            <div className="who">
              <Avatar name={name} seed={c.id} size={44} />
              <div style={{ minWidth: 0 }}>
                <Link className="who-name" to={`/admin/customers/${c.id}`}>
                  {name}
                </Link>
                {handle && <div className="who-sub">{handle}</div>}
              </div>
            </div>
            <div className="info-list">
              {c.telegram_id && (
                <InfoRow icon={Send} label="Telegram ID">
                  <span className="num">{c.telegram_id}</span>
                </InfoRow>
              )}
              <InfoRow icon={Calendar} label="Клиент с">
                {date(c.created_at)}
              </InfoRow>
            </div>
            {v.subscriptions.length === 0 ? (
              <p className="small muted" style={{ margin: 0 }}>
                Подписок нет
              </p>
            ) : (
              v.subscriptions.map((s) => {
                const expired = new Date(s.expires_at) < new Date()
                return (
                  <div key={s.id} className={expired ? 'mini-sub bad' : 'mini-sub'}>
                    <div className="top">
                      <span className="ellipsis">{s.plan_name || s.title}</span>
                      <span className="badge">{s.kind === 'trial' ? 'пробная' : s.kind === 'free' ? 'бесплатная' : 'платная'}</span>
                    </div>
                    <div className="sub">
                      <b>{expired ? 'истекла ' : 'до '}{date(s.expires_at)}</b> · устройств {s.devices_used}/{s.device_limit ? s.device_limit + s.extra_devices : '∞'}
                    </div>
                  </div>
                )
              })
            )}
            <Link className="btn secondary small" to={`/admin/customers/${c.id}`}>
              Открыть карточку клиента
            </Link>
          </Panel>

          <Panel icon={Sliders} tone="amber" title="Обращение">
            <div className="ticket-info">
              <div className="ctl">
                <select className="input" value={v.status} onChange={(e) => setField({ status: e.target.value }, 'Статус изменён')} aria-label="Статус">
                  {Object.entries(ticketStatus).map(([k, [l]]) => (
                    <option key={k} value={k}>
                      {l}
                    </option>
                  ))}
                </select>
                <input className="input" placeholder="Тег" defaultValue={v.tag} onBlur={(e) => e.target.value !== v.tag && setField({ tag: e.target.value })} aria-label="Тег обращения" />
              </div>
              <Toggle checked={v.ai_enabled} onChange={(on) => setField({ ai_enabled: on }, on ? 'Ассистент включён' : 'Ассистент выключен')} label="Ассистент отвечает" hint="Выключается сам, когда вы отвечаете" />
            </div>
          </Panel>
        </aside>
      </div>
    </div>
  )
}
