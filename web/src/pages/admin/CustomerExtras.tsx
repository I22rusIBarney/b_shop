import { useNavigate } from 'react-router-dom'
import { get, post, type AutomationRun, type TicketRow } from '../../lib/api'
import { dateTime, shortDate } from '../../lib/format'
import { ticketStatus } from '../../ui/chat'
import { useAction, useLoad } from '../../ui/kit'

const stopReason: Record<string, string> = {
  purchase: 'оплатил',
  admin: 'остановлен вручную',
  disabled: 'сценарий выключен',
  deleted: 'сценарий удалён',
  condition: 'клиент заблокирован',
  unreachable: 'некуда отправить',
}

/** Tickets and automation chains of one customer, for the customer card. */
export default function CustomerExtras({ customerId }: { customerId: number }) {
  const nav = useNavigate()
  const tickets = useLoad(() => get<{ items: TicketRow[] }>(`/api/admin/tickets?status=all&customer=${customerId}`), [customerId])
  const runs = useLoad(() => get<AutomationRun[]>(`/api/admin/customers/${customerId}/automations`), [customerId])
  const { busy, run } = useAction()
  const active = (runs.data || []).filter((r) => r.status === 'active')

  const stop = (runId: number) =>
    run(async () => {
      await post(`/api/admin/customers/${customerId}/automations/stop`, { run_id: runId })
      await runs.reload()
    }, 'Остановлено')

  return (
    <>
      <section className="card stack">
        <h3>Обращения</h3>
        {!tickets.data?.items.length ? (
          <p className="small muted">Обращений не было</p>
        ) : (
          <div className="list small">
            {tickets.data.items.map((t) => {
              const [label, cls] = ticketStatus[t.status]
              return (
                <div className="item item-link" key={t.id} onClick={() => nav(`/admin/tickets/${t.id}`)}>
                  <span className="grow ellipsis">
                    #{t.id} {t.subject}
                  </span>
                  {t.rating && <span className="badge">★ {t.rating}</span>}
                  <span className={cls}>{label}</span>
                  <span className="tiny muted">{shortDate(t.created_at)}</span>
                </div>
              )
            })}
          </div>
        )}
      </section>

      <section className="card stack">
        <div className="spread">
          <h3>Автоматизации</h3>
          {active.length > 0 && (
            <button className="btn ghost small" disabled={busy} onClick={() => stop(0)}>
              Остановить все ({active.length})
            </button>
          )}
        </div>
        {!runs.data?.length ? (
          <p className="small muted">В цепочки не попадал</p>
        ) : (
          <div className="list small">
            {runs.data.map((r) => (
              <div className="item" key={r.id}>
                <span className="grow">
                  {r.automation_name}{' '}
                  <span className="muted">
                    · отправлено {r.sent}
                    {r.status === 'active' && r.next_at ? ` · следующее ${dateTime(r.next_at)}` : ''}
                    {r.status === 'stopped' ? ` · ${stopReason[r.stop_reason] || r.stop_reason}` : ''}
                  </span>
                </span>
                {r.converted && <span className="badge ok">конверсия</span>}
                {r.status === 'active' ? (
                  <button className="btn ghost small" disabled={busy} onClick={() => stop(r.id)}>
                    Стоп
                  </button>
                ) : (
                  <span className="badge">{r.status === 'done' ? 'завершена' : 'остановлена'}</span>
                )}
              </div>
            ))}
          </div>
        )}
      </section>
    </>
  )
}
