import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { uploadFile, type SupportTemplate, type TicketMessage, type UploadedFile } from '../lib/api'
import { clock, dateTime, dayLabel } from '../lib/format'
import { Close, Paperclip, Plus, Send } from './icons'
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
  const seen = useRef(0)
  useEffect(() => {
    const el = end.current
    const first = seen.current === 0
    const added = messages.length > seen.current
    seen.current = messages.length
    if (!el || !added) return
    // Open the chat at the latest message. Later, follow new messages only when the reader is already
    // near the end or just sent one; don't yank the page while they read history or type a reply.
    const last = messages[messages.length - 1]
    const own = viewer === 'customer' ? last?.author === 'customer' : last?.author === 'operator' || last?.author === 'note'
    const typing = document.activeElement instanceof HTMLTextAreaElement || document.activeElement instanceof HTMLInputElement
    const box0 = el.closest('.tk-scroll') as HTMLElement | null
    const near = box0 ? box0.scrollHeight - box0.scrollTop - box0.clientHeight < 240 : el.getBoundingClientRect().top < window.innerHeight + 320
    if (!(first || own || (near && !typing))) return
    // Inside a scrolling thread (admin ticket) move that box, not the page; repeat after layout settles (fonts, images).
    const box = el.closest('.tk-scroll') as HTMLElement | null
    if (box) {
      const down = () => box.scrollTo({ top: box.scrollHeight, behavior: first ? 'instant' : 'smooth' })
      down()
      if (first) [60, 250, 600].forEach((ms) => setTimeout(down, ms))
      return
    }
    el.scrollIntoView({ block: 'end', behavior: first ? 'auto' : 'smooth' })
  }, [messages, viewer])
  // Thread box (admin ticket): stay pinned to the newest message while the reader is at the bottom, even when the box
  // or its content changes size — fonts loading, the reply bar growing, the phone keyboard opening.
  useEffect(() => {
    const el = end.current
    const box = el?.closest('.tk-scroll') as HTMLElement | null
    if (!box) return
    let pinned = true
    const onScroll = () => (pinned = box.scrollHeight - box.scrollTop - box.clientHeight < 24)
    const ro = new ResizeObserver(() => pinned && (box.scrollTop = box.scrollHeight))
    ro.observe(box)
    if (box.firstElementChild) ro.observe(box.firstElementChild)
    box.addEventListener('scroll', onScroll, { passive: true })
    return () => {
      ro.disconnect()
      box.removeEventListener('scroll', onScroll)
    }
  }, [])
  return (
    <div className="chat">
      {messages.map((m, i) => {
        // A separator before the first message of every day; bubbles then show only the time.
        const day = new Date(m.created_at).toDateString()
        const sep = i === 0 || new Date(messages[i - 1].created_at).toDateString() !== day ? (
          <div className="day-sep" role="separator">
            <span>{dayLabel(m.created_at)}</span>
          </div>
        ) : null
        if (m.author === 'system')
          return (
            <Fragment key={m.id}>
              {sep}
              <div className="msg system">
                {m.body} · {clock(m.created_at)}
              </div>
            </Fragment>
          )
        const mine = viewer === 'customer' ? m.author === 'customer' : m.author === 'operator' || m.author === 'ai'
        const cls = m.author === 'note' ? 'msg note' : m.author === 'draft' ? 'msg draft' : mine ? 'msg me' : 'msg them'
        let who = authorName[m.author] || ''
        if (viewer === 'admin' && m.author === 'ai') who = 'Ассистент (ИИ)'
        if (m.author === 'draft' && m.draft_status !== 'pending' && viewer === 'admin') who = `Черновик ассистента · ${m.draft_status === 'sent' ? 'отправлен' : 'отклонён'}`
        if (viewer === 'customer' && m.author === 'customer') who = 'Вы'
        return (
          <Fragment key={m.id}>
          {sep}
          <div className={cls}>
            <div className="meta">
              <span>{who}</span>
              <span title={dateTime(m.created_at)}>{clock(m.created_at)}</span>
              {viewer === 'admin' && m.confidence != null && <span>уверенность {m.confidence.toFixed(2)}</span>}
            </div>
            {!(m.author === 'draft' && m.draft_status === 'pending' && renderExtra) && m.body}
            <Attachments ids={m.attachments} />
            {renderExtra?.(m)}
          </div>
          </Fragment>
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
  bar,
}: {
  onSend: (body: string, files: string[]) => Promise<boolean | void>
  maxFiles?: number
  admin?: boolean
  templates?: SupportTemplate[]
  placeholder?: string
  extra?: ReactNode
  value?: string
  onChange?: (v: string) => void
  /** Messenger-style box: auto-growing text with the toolbar inside it (admin ticket thread). */
  bar?: boolean
}) {
  const [own, setOwn] = useState('')
  const text = value ?? own
  const setText = onChange ?? setOwn
  const [files, setFiles] = useState<UploadedFile[]>([])
  const [pick, setPick] = useState(0)
  const { busy, run } = useAction()
  const input = useRef<HTMLInputElement>(null)
  const area = useRef<HTMLTextAreaElement>(null)
  // The bar grows with the text up to a limit, then scrolls inside.
  useLayoutEffect(() => {
    const ta = area.current
    if (!bar || !ta) return
    ta.style.height = 'auto'
    ta.style.height = `${Math.min(ta.scrollHeight, 220)}px`
  }, [text, bar])

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
    <div className={bar ? 'composer bar' : 'composer'}>
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
      <div className={bar ? 'cbox' : 'contents'}>
      <textarea
        ref={area}
        className={bar ? 'cbox-text' : 'input'}
        rows={bar ? 1 : undefined}
        placeholder={admin ? `${placeholder} · «/» — шаблоны` : placeholder}
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
      <div className={bar ? 'ctools' : 'row wrap'}>
        {maxFiles > 0 && (
          <>
            <input ref={input} type="file" hidden multiple accept="image/*,application/pdf,text/plain,video/mp4" onChange={(e) => attach(e.target.files)} />
            {bar ? (
              <button className="tool-btn" disabled={busy || files.length >= maxFiles} onClick={() => input.current?.click()} aria-label="Прикрепить файл" title="Прикрепить файл">
                <Paperclip />
              </button>
            ) : (
              <button className="btn secondary small" disabled={busy || files.length >= maxFiles} onClick={() => input.current?.click()}>
                <Plus /> Файл
              </button>
            )}
          </>
        )}
        {extra}
        <button
          className={bar ? 'send-btn' : 'btn small'}
          style={bar ? undefined : { marginLeft: 'auto' }}
          disabled={busy || (!text.trim() && !files.length)}
          onClick={send}
          title="Отправить (Ctrl/⌘ + Enter)"
        >
          {bar && <Send />}
          Отправить
        </button>
      </div>
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
