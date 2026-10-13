import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { uploadFile, type SupportTemplate, type TicketMessage, type UploadedFile } from '../lib/api'
import { dateTime } from '../lib/format'
import { Close, Plus } from './icons'
import { useAction } from './kit'

const authorName: Record<string, string> = {
  customer: 'Клиент',
  operator: 'Поддержка',
  ai: 'Поддержка',
  note: 'Заметка',
  draft: 'Черновик ассистента',
}

function Attachments({ ids }: { ids: string[] }) {
  if (!ids?.length) return null
  return (
    <div className="atts">
      {ids.map((id) => (
        <a key={id} href={`/files/${id}`} target="_blank" rel="noreferrer">
          <img
            src={`/files/${id}`}
            alt="Вложение"
            loading="lazy"
            onError={(e) => {
              // Not an image: show a file link instead.
              const a = e.currentTarget.parentElement as HTMLAnchorElement
              e.currentTarget.remove()
              a.className = 'file'
              a.textContent = 'Файл'
            }}
          />
        </a>
      ))}
    </div>
  )
}

/** Message list. viewer=customer shows the customer's own messages on the right; admin sees support on the right. */
export function Messages({ messages, viewer, renderExtra }: { messages: TicketMessage[]; viewer: 'customer' | 'admin'; renderExtra?: (m: TicketMessage) => ReactNode }) {
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    end.current?.scrollIntoView({ block: 'end' })
  }, [messages.length])
  return (
    <div className="chat">
      {messages.map((m) => {
        if (m.author === 'system')
          return (
            <div key={m.id} className="msg system">
              {m.body} · {dateTime(m.created_at)}
            </div>
          )
        const mine = viewer === 'customer' ? m.author === 'customer' : m.author === 'operator' || m.author === 'ai'
        const cls = m.author === 'note' ? 'msg note' : m.author === 'draft' ? 'msg draft' : mine ? 'msg me' : 'msg them'
        let who = authorName[m.author] || ''
        if (viewer === 'admin' && m.author === 'ai') who = 'Ассистент (ИИ)'
        if (m.author === 'draft' && m.draft_status !== 'pending' && viewer === 'admin') who = `Черновик ассистента · ${m.draft_status === 'sent' ? 'отправлен' : 'отклонён'}`
        if (viewer === 'customer' && m.author === 'customer') who = 'Вы'
        return (
          <div key={m.id} className={cls}>
            <div className="meta">
              <span>{who}</span>
              <span>{dateTime(m.created_at)}</span>
              {viewer === 'admin' && m.confidence != null && <span>уверенность {m.confidence.toFixed(2)}</span>}
            </div>
            {!(m.author === 'draft' && m.draft_status === 'pending' && renderExtra) && m.body}
            <Attachments ids={m.attachments} />
            {renderExtra?.(m)}
          </div>
        )
      })}
      <div ref={end} />
    </div>
  )
}

/** Text box with attachments; in admin mode typing "/" opens reply templates. */
export function Composer({
  onSend,
  maxFiles = 5,
  admin,
  templates,
  placeholder = 'Сообщение',
  extra,
  value,
  onChange,
}: {
  onSend: (body: string, files: string[]) => Promise<boolean | void>
  maxFiles?: number
  admin?: boolean
  templates?: SupportTemplate[]
  placeholder?: string
  extra?: ReactNode
  value?: string
  onChange?: (v: string) => void
}) {
  const [own, setOwn] = useState('')
  const text = value ?? own
  const setText = onChange ?? setOwn
  const [files, setFiles] = useState<UploadedFile[]>([])
  const [pick, setPick] = useState(0)
  const { busy, run } = useAction()
  const input = useRef<HTMLInputElement>(null)

  const query = text.startsWith('/') && admin ? text.slice(1).toLowerCase() : null
  const matches = useMemo(
    () => (query === null || !templates ? [] : templates.filter((t) => (t.title + ' ' + t.category).toLowerCase().includes(query)).slice(0, 8)),
    [query, templates],
  )

  const send = () =>
    run(async () => {
      if (!text.trim() && !files.length) return
      const ok = await onSend(text, files.map((f) => f.id))
      if (ok !== false) {
        setText('')
        setFiles([])
      }
    })

  const attach = (list: FileList | null) =>
    run(async () => {
      if (!list) return
      for (const f of Array.from(list).slice(0, maxFiles - files.length)) {
        const up = await uploadFile(f, admin)
        setFiles((x) => [...x, up])
      }
    })

  return (
    <div className="composer">
      {matches.length > 0 && (
        <div className="tpl-pop" role="listbox" aria-label="Шаблоны">
          {matches.map((t, i) => (
            <button key={t.id} className={i === pick ? 'on' : ''} onClick={() => setText(t.body)}>
              <b>{t.title}</b>
              {t.category && <span className="tiny muted"> · {t.category}</span>}
            </button>
          ))}
        </div>
      )}
      <textarea
        className="input"
        placeholder={admin ? `${placeholder} («/» — шаблоны)` : placeholder}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          setPick(0)
        }}
        onKeyDown={(e) => {
          if (matches.length) {
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setPick((p) => (p + 1) % matches.length)
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setPick((p) => (p - 1 + matches.length) % matches.length)
            } else if (e.key === 'Enter') {
              e.preventDefault()
              setText(matches[pick].body)
            }
            return
          }
          if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
        }}
      />
      {files.length > 0 && (
        <div className="pending">
          {files.map((f) => (
            <span key={f.id}>
              {f.name}
              <button onClick={() => setFiles((x) => x.filter((y) => y.id !== f.id))} aria-label="Убрать файл">
                <Close />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="row wrap">
        {maxFiles > 0 && (
          <>
            <input ref={input} type="file" hidden multiple accept="image/*,application/pdf,text/plain,video/mp4" onChange={(e) => attach(e.target.files)} />
            <button className="btn secondary small" disabled={busy || files.length >= maxFiles} onClick={() => input.current?.click()}>
              <Plus /> Файл
            </button>
          </>
        )}
        {extra}
        <button className="btn small" style={{ marginLeft: 'auto' }} disabled={busy || (!text.trim() && !files.length)} onClick={send}>
          Отправить
        </button>
      </div>
    </div>
  )
}

export const ticketStatus: Record<string, [string, string]> = {
  open: ['открыто', 'badge accent'],
  waiting: ['ждём ответа', 'badge warn'],
  resolved: ['решено', 'badge ok'],
  closed: ['закрыто', 'badge'],
}

export const customerTicketStatus: Record<string, [string, string]> = {
  open: ['на рассмотрении', 'badge accent'],
  waiting: ['есть ответ', 'badge ok'],
  resolved: ['решено', 'badge ok'],
  closed: ['закрыто', 'badge'],
}
