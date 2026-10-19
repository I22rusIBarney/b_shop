import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { get, post, type FAQItem, type Subscription, type SupportConfig, type Ticket } from '../lib/api'
import { useApp } from '../lib/app'
import { relative } from '../lib/format'
import { openLink } from '../lib/tg'
import { Chat, Chevron, Devices, Help, News, Plus } from '../ui/icons'
import { Composer, customerTicketStatus } from '../ui/chat'
import { ErrorBox, Field, PageLoader, Sheet, useLoad, usePolling } from '../ui/kit'

export default function Support() {
  const { config } = useApp()
  const nav = useNavigate()
  const cfg = useLoad(() => get<SupportConfig>('/api/support/config'))
  const list = useLoad(() => get<Ticket[]>('/api/tickets'))
  const subs = useLoad(() => get<Subscription[]>('/api/subscriptions'))

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
  const primary = subs.data?.find((x) => x.is_primary) || subs.data?.[0]
  const shown = tickets.filter((t) => (tab === 'open' ? t.status !== 'closed' : t.status === 'closed'))

  return (
    <div className="stack lg">
      <h1>Поддержка</h1>

      <div className="card list action-list">
        <button type="button" className="item item-link" onClick={() => setCreating(true)}>
          <span className="action-ico">
            <Plus />
          </span>
          <span className="grow">
            <b>Новое обращение</b>
            <span className="tiny muted">Связаться с поддержкой</span>
          </span>
        </button>
        {c.faq.length > 0 && (
          <Link className="item item-link" to="/faq">
            <span className="action-ico">
              <Help />
            </span>
            <span className="grow">
              <b>Часто задаваемые вопросы</b>
              <span className="tiny muted">Ответы на часто задаваемые вопросы</span>
            </span>
          </Link>
        )}
        {primary && (
          <Link className="item item-link" to={`/install/${primary.id}`}>
            <span className="action-ico">
              <Devices />
            </span>
            <span className="grow">
              <b>Установка на другом устройстве</b>
              <span className="tiny muted">Подробная инструкция для установки</span>
            </span>
          </Link>
        )}
        <Link className="item item-link mobile-only" to="/news">
          <span className="action-ico">
            <News />
          </span>
          <span className="grow">
            <b>Новости</b>
            <span className="tiny muted">Объявления и технические работы</span>
          </span>
        </Link>
      </div>

      <div className="seg seg-fill seg-2" role="tablist" aria-label="Обращения">
        <button role="tab" aria-selected={tab === 'open'} className={tab === 'open' ? 'on' : ''} onClick={() => setTab('open')}>
          Открытые
        </button>
        <button role="tab" aria-selected={tab === 'history'} className={tab === 'history' ? 'on' : ''} onClick={() => setTab('history')}>
          История
        </button>
      </div>

      {shown.length === 0 ? (
        <div className="card empty-state">
          <Chat />
          <p className="small">{tab === 'open' ? 'Открытых обращений нет. Если что-то не работает — напишите нам.' : 'Здесь появятся закрытые обращения.'}</p>
        </div>
      ) : (
        <div className="stack">
          {shown.map((t) => {
            const [label, cls] = customerTicketStatus[t.status]
            return (
              <Link key={t.id} className="card ticket-row" to={`/support/${t.id}`}>
                <div className="grow" style={{ minWidth: 0 }}>
                  <b className="item-title">{t.subject}</b>
                  <div className="item-meta tiny muted">
                    <span className="num">
                      #{t.id} · {relative(t.updated_at)}
                    </span>
                    {t.unread_customer && <span className="unread-dot" aria-label="новый ответ" />}
                  </div>
                </div>
                <span className={cls}>{label}</span>
                <Chevron className="chev" />
              </Link>
            )
          })}
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

export function FaqList({ items, title = 'Частые вопросы' }: { items: FAQItem[]; title?: string }) {
  const sections = Array.from(new Set(items.map((f) => f.section)))
  return (
    <div className="card faq">
      {title && <h2 style={{ marginBottom: 8 }}>{title}</h2>}
      {sections.map((s) => (
        <div key={s}>
          {s && <div className="section-title">{s}</div>}
          {items
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
  )
}

/** Standalone FAQ (desktop menu → FAQ). Same items the support page shows. */
export function Faq() {
  const cfg = useLoad(() => get<SupportConfig>('/api/support/config'))
  if (cfg.loading && !cfg.data) return <PageLoader />
  if (cfg.error) return <ErrorBox text={cfg.error} retry={cfg.reload} />
  const faq = cfg.data!.faq
  return (
    <div className="stack lg">
      <h1>FAQ</h1>
      {faq.length ? (
        <FaqList items={faq} title="" />
      ) : (
        <div className="card empty-state">
          <Help />
          <p>Нет вопросов</p>
        </div>
      )}
    </div>
  )
}
