import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { get, patch, post, type AdminTicketView, type SupportTemplate, type TicketMessage, type TicketRow } from '../../lib/api'
import { refreshBadges } from '../../lib/badges'
import { date, relative, shortDate } from '../../lib/format'
import { Avatar, Empty, Switcher } from '../../ui/admin'
import { Back, Bolt, Chat, ChevronDown, Copy } from '../../ui/icons'
import { Composer, Messages, ticketStatus } from '../../ui/chat'
import { ErrorBox, Spinner, Toggle, copy, useAction, useLoad, usePolling, useToast } from '../../ui/kit'

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
      <div className="filters toolbar">
        <Switcher
          label="Статус обращений"
          options={tabs.map(([value, label]) => ({ value, label }))}
          value={status}
          onChange={(v) => (setStatus(v as typeof status), setOffset(0))}
        />
        <input
          className="input grow"
          type="search"
          placeholder="Поиск: номер, тема, клиент, Telegram ID"
          value={q}
          onChange={(e) => (setQ(e.target.value), setOffset(0))}
          aria-label="Поиск обращений"
        />
      </div>
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
  const toast = useToast()
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
  const handle = c.username ? '@' + c.username : c.email || ''
  const copyId = async () => c.telegram_id && (await copy(String(c.telegram_id))) && toast('Telegram ID скопирован')

  return (
    <div className="tk-page">
      <header className="tk-top">
        <button className="icon-btn" onClick={() => nav('/admin/tickets')} aria-label="К списку обращений">
          <Back />
        </button>
        <Link className="tk-client" to={`/admin/customers/${c.id}`} title="Открыть карточку клиента">
          <Avatar name={name} seed={c.id} size={42} />
          <span className="tk-client-text">
            <b>{name}</b>
            <span>{[handle, c.telegram_id && `ID ${c.telegram_id}`].filter(Boolean).join(' · ') || 'без Telegram'}</span>
          </span>
        </Link>
        {c.telegram_id && (
          <button className="copy-btn" onClick={copyId} aria-label="Скопировать Telegram ID" title="Скопировать Telegram ID">
            <Copy />
          </button>
        )}
        <div className="tk-subs" aria-label="Подписки клиента">
          {v.subscriptions.length === 0 ? (
            <span className="sub-chip none">Без подписки</span>
          ) : (
            v.subscriptions.map((s) => {
              const expired = new Date(s.expires_at) < new Date()
              return (
                <span key={s.id} className={expired ? 'sub-chip bad' : 'sub-chip'} title={s.kind === 'trial' ? 'Пробная подписка' : undefined}>
                  <b>{s.plan_name || s.title}</b>
                  <span>
                    {expired ? 'истекла ' : 'до '}
                    {shortDate(s.expires_at)} · {s.devices_used}/{s.device_limit ? s.device_limit + s.extra_devices : '∞'} устр.
                    {s.kind === 'trial' && ' · пробная'}
                  </span>
                </span>
              )
            })
          )}
        </div>
        <div className="tk-controls">
          <label className={`status-select status ${v.status}`}>
            <select value={v.status} onChange={(e) => setField({ status: e.target.value }, 'Статус изменён')} aria-label="Статус обращения">
              {Object.entries(ticketStatus).map(([k, [l]]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
            <ChevronDown />
          </label>
          <Toggle checked={v.ai_enabled} onChange={(on) => setField({ ai_enabled: on }, on ? 'Ассистент включён' : 'Ассистент выключен')} label="ИИ" />
        </div>
      </header>

      <section className="tk-thread">
        <div className="tk-thread-head">
          <div className="tk-subject">
            <span className="muted num">#{v.id}</span>
            <b>{v.subject}</b>
          </div>
          <div className="tk-thread-meta">
            <span>
              {date(v.created_at)} · {v.source === 'telegram' || v.source === 'bot' ? 'из бота' : 'из кабинета'}
            </span>
            {v.rating && (
              <span className="tk-flag" title={v.rating_comment || undefined}>
                ★ {v.rating}
              </span>
            )}
            <input className="tag-input" placeholder="# тег" defaultValue={v.tag} onBlur={(e) => e.target.value !== v.tag && setField({ tag: e.target.value })} aria-label="Тег обращения" />
          </div>
        </div>

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
            bar
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
                <button className="tool-btn" disabled={busy} onClick={suggest} title="Подсказка ИИ: черновик ответа в поле" aria-label="Подсказка ИИ">
                  <Bolt />
                </button>
                <div className="seg mini" role="tablist" aria-label="Тип сообщения">
                  <button role="tab" aria-selected={!note} className={!note ? 'on' : ''} onClick={() => setNote(false)}>
                    Ответ
                  </button>
                  <button role="tab" aria-selected={note} className={note ? 'on' : ''} onClick={() => setNote(true)}>
                    Заметка
                  </button>
                </div>
                {!note && (
                  <select className="after-select" value={after} onChange={(e) => setAfter(e.target.value)} aria-label="Статус после ответа">
                    <option value="waiting">→ ждём клиента</option>
                    <option value="resolved">→ решено</option>
                    <option value="open">→ оставить открытым</option>
                  </select>
                )}
              </>
            }
          />
        </div>
      </section>
    </div>
  )
}
