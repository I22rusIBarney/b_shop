import { useNavigate } from 'react-router-dom'
import { get, post, type AutomationRun, type TicketRow } from '../../lib/api'
import { dateTime, shortDate } from '../../lib/format'
import { ticketStatus } from '../../ui/chat'
import { useAction, useLoad } from '../../ui/kit'
import { Empty, Panel } from '../../ui/admin'
import { Chat, Renew } from '../../ui/icons'

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
      <Panel icon={Chat} tone="green" title="Обращения" count={tickets.data?.items.length}>
        {!tickets.data?.items.length ? (
          <Empty icon={Chat}>Обращений не было</Empty>
        ) : (
          <div className="feed">
            {tickets.data.items.map((t) => {
              const [label] = ticketStatus[t.status]
              return (
                <button type="button" className="feed-item feed-link" key={t.id} onClick={() => nav(`/admin/tickets/${t.id}`)}>
                  <span className="ic" style={{ ['--c' as string]: '#4fcf8a' }}>
                    <Chat />
                  </span>
                  <div className="main">
                    <div>
                      <span className="muted num">#{t.id}</span> {t.subject}
                    </div>
                    <div className="s">
                      {shortDate(t.created_at)}
                      {t.rating ? ` · ★ ${t.rating}` : ''}
                    </div>
                  </div>
                  <div className="end">
                    <span className={`status ${t.status}`}>{label}</span>
                  </div>
                </button>
              )
            })}
          </div>
        )}
      </Panel>

      <Panel
        icon={Renew}
        tone="teal"
        title="Автоматизации"
        count={runs.data?.length}
        actions={
          active.length > 0 && (
            <button className="btn ghost small" disabled={busy} onClick={() => stop(0)}>
              Остановить все ({active.length})
            </button>
          )
        }
      >
        {!runs.data?.length ? (
          <Empty icon={Renew}>В цепочки не попадал</Empty>
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
      </Panel>
    </>
  )
}
