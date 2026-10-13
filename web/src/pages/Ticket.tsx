import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, type SupportConfig, type TicketView } from '../lib/api'
import { haptic } from '../lib/tg'
import { Back } from '../ui/icons'
import { Composer, Messages, customerTicketStatus } from '../ui/chat'
import { ErrorBox, PageLoader, useAction, useLoad, usePolling } from '../ui/kit'

export default function Ticket() {
  const { id } = useParams()
  const nav = useNavigate()
  const t = useLoad(() => get<TicketView>(`/api/tickets/${id}`), [id])
  const cfg = useLoad(() => get<SupportConfig>('/api/support/config'))
  const { busy, run } = useAction()
  const [stars, setStars] = useState(0)
  const [comment, setComment] = useState('')

  // Pick up replies while the chat is open; the text being typed lives in the composer and is not touched.
  usePolling(t.refresh, 8000)

  if (t.loading && !t.data) return <PageLoader />
  if (t.error && !t.data) return <ErrorBox text={t.error} retry={t.reload} />
  const v = t.data!
  const [label, cls] = customerTicketStatus[v.status]
  const done = v.status === 'closed' || v.status === 'resolved'

  return (
    <div className="stack lg">
      <div className="row">
        <button className="icon-btn" onClick={() => nav('/support')} aria-label="Назад">
          <Back />
        </button>
        <div className="grow" style={{ minWidth: 0 }}>
          <h1 className="ellipsis">{v.subject}</h1>
          <div className="row" style={{ gap: 8 }}>
            <span className="tiny muted">Обращение #{v.id}</span>
            <span className={cls}>{label}</span>
          </div>
        </div>
      </div>

      <div className="card">
        <Messages messages={v.messages} viewer="customer" />
      </div>

      {v.status !== 'closed' && (
        <div className="card stack">
          <Composer
            maxFiles={cfg.data?.max_files ?? 5}
            placeholder="Ваше сообщение"
            onSend={async (body, attachments) => {
              await post(`/api/tickets/${v.id}/messages`, { body, attachments })
              haptic('success')
              await t.reload()
            }}
          />
          <button
            className="btn ghost small"
            disabled={busy}
            onClick={() =>
              run(async () => {
                await post(`/api/tickets/${v.id}/close`)
                await t.reload()
              }, 'Обращение закрыто')
            }
          >
            Вопрос решён — закрыть обращение
          </button>
        </div>
      )}

      {done && cfg.data?.rating && (
        <div className="card stack">
          {v.rating ? (
            <p className="small">
              Спасибо за оценку: {'★'.repeat(v.rating)}
              {'☆'.repeat(5 - v.rating)}
            </p>
          ) : (
            <>
              <h2>Как вам поддержка?</h2>
              <div className="stars" role="radiogroup" aria-label="Оценка">
                {[1, 2, 3, 4, 5].map((n) => (
                  <button key={n} className={n <= stars ? 'on' : ''} onClick={() => setStars(n)} aria-label={`${n} из 5`}>
                    ★
                  </button>
                ))}
              </div>
              <textarea className="input" placeholder="Комментарий (необязательно)" value={comment} onChange={(e) => setComment(e.target.value)} />
              <button
                className="btn"
                disabled={!stars || busy}
                onClick={() =>
                  run(async () => {
                    await post(`/api/tickets/${v.id}/rate`, { rating: stars, comment })
                    await t.reload()
                  }, 'Спасибо!')
                }
              >
                Оценить
              </button>
            </>
          )}
          {v.status === 'closed' && <p className="tiny muted">Если проблема вернулась — создайте новое обращение.</p>}
        </div>
      )}
    </div>
  )
}
