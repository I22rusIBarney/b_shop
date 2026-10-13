import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { get, post, type SupportConfig, type Ticket } from '../lib/api'
import { useApp } from '../lib/app'
import { relative } from '../lib/format'
import { openLink } from '../lib/tg'
import { Chat, Chevron, Plus } from '../ui/icons'
import { Composer, customerTicketStatus } from '../ui/chat'
import { ErrorBox, Field, PageLoader, Sheet, useLoad, usePolling } from '../ui/kit'

export default function Support() {
  const { config } = useApp()
  const nav = useNavigate()
  const cfg = useLoad(() => get<SupportConfig>('/api/support/config'))
  const list = useLoad(() => get<Ticket[]>('/api/tickets'))

  usePolling(list.refresh, 10000)
  const [tab, setTab] = useState<'open' | 'history'>('open')
  const [creating, setCreating] = useState(false)
  const [subject, setSubject] = useState('')
  const supportLink = config.links.find((l) => l.kind === 'support')

  if ((cfg.loading && !cfg.data) || (list.loading && !list.data)) return <PageLoader />
  if (cfg.error) return <ErrorBox text={cfg.error} retry={cfg.reload} />
  const c = cfg.data!

  if (!c.enabled)
    return (
      <div className="card stack lg center">
        <div style={{ display: 'grid', placeItems: 'center', color: 'var(--accent)' }}>
          <Chat style={{ width: 40, height: 40 }} />
        </div>
        <h1>Поддержка</h1>
        {supportLink ? (
          <button className="btn block" onClick={() => openLink(supportLink.url)}>
            Написать в поддержку
          </button>
        ) : (
          <p className="small muted">Контакт поддержки появится здесь, когда администратор его добавит.</p>
        )}
      </div>
    )

  const tickets = list.data || []
  const shown = tickets.filter((t) => (tab === 'open' ? t.status !== 'closed' : t.status === 'closed'))
  const sections = Array.from(new Set(c.faq.map((f) => f.section)))

  return (
    <div className="stack lg">
      <div className="spread wrap">
        <h1>Поддержка</h1>
        <button className="btn small" onClick={() => setCreating(true)}>
          <Plus /> Новое обращение
        </button>
      </div>

      <div className="row wrap">
        <Link className="btn secondary small" to="/news">
          Новости
        </Link>
      </div>

      <div className="card">
        <div className="chips" role="tablist">
          <button className={tab === 'open' ? 'chip on' : 'chip'} onClick={() => setTab('open')}>
            Открытые
          </button>
          <button className={tab === 'history' ? 'chip on' : 'chip'} onClick={() => setTab('history')}>
            История
          </button>
        </div>
        {shown.length === 0 ? (
          <p className="small muted">{tab === 'open' ? 'Открытых обращений нет. Если что-то не работает — напишите нам.' : 'Здесь появятся закрытые обращения.'}</p>
        ) : (
          <div className="list">
            {shown.map((t) => {
              const [label, cls] = customerTicketStatus[t.status]
              return (
                <Link key={t.id} className="item item-link" to={`/support/${t.id}`}>
                  <div className="grow" style={{ minWidth: 0 }}>
                    <b className="item-title">{t.subject}</b>
                    <div className="item-meta tiny muted">
                      <span className="num">
                        #{t.id} · {relative(t.updated_at)}
                      </span>
                      <span className={cls}>{label}</span>
                      {t.unread_customer && <span className="unread-dot" aria-label="новый ответ" />}
                    </div>
                  </div>
                  <Chevron className="chev" />
                </Link>
              )
            })}
          </div>
        )}
      </div>

      {c.faq.length > 0 && (
        <div className="card faq">
          <h2 style={{ marginBottom: 8 }}>Частые вопросы</h2>
          {sections.map((s) => (
            <div key={s}>
              {s && <div className="section-title">{s}</div>}
              {c.faq
                .filter((f) => f.section === s)
                .map((f) => (
                  <details key={f.id}>
                    <summary>{f.question}</summary>
                    <p>{f.answer}</p>
                  </details>
                ))}
            </div>
          ))}
        </div>
      )}

      <Sheet open={creating} onClose={() => setCreating(false)} title="Новое обращение" icon={<Chat />}>
        <div className="stack">
          {c.faq.length > 0 && <p className="small muted">Загляните в частые вопросы — возможно, ответ уже есть.</p>}
          <Field label="Тема" hint="Необязательно">
            <input className="input" value={subject} onChange={(e) => setSubject(e.target.value)} maxLength={120} placeholder="Например: не подключается на iPhone" />
          </Field>
          <Composer
            maxFiles={c.max_files}
            placeholder="Опишите, что случилось, на каком устройстве и в каком приложении"
            onSend={async (body, attachments) => {
              const t = await post<Ticket>('/api/tickets', { subject, body, attachments })
              setCreating(false)
              setSubject('')
              nav(`/support/${t.id}`)
            }}
          />
        </div>
      </Sheet>
    </div>
  )
}
