import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { get, type Paged, type Payment } from '../../lib/api'
import { dateTime, operationName, paymentStatus, rub } from '../../lib/format'
import { ErrorBox, Sheet, Spinner, useLoad, pressable } from '../../ui/kit'
import { Avatar, Pager } from '../../ui/admin'

const PAGE = 50
const receiptName: Record<string, string> = { none: '—', pending: 'в очереди', done: 'отправлен', failed: 'ошибка', cancel_pending: 'аннулируется', canceled: 'аннулирован' }

export default function Payments() {
  const [params, setParams] = useSearchParams()
  const nav = useNavigate()
  const [q, setQ] = useState(params.get('q') || '')
  const [open, setOpen] = useState<Payment | null>(null)
  const offset = Number(params.get('offset')) || 0
  const qs = new URLSearchParams({ q: params.get('q') || '', status: params.get('status') || '', from: params.get('from') || '', to: params.get('to') || '', limit: String(PAGE), offset: String(offset) })
  const data = useLoad(() => get<Paged<Payment>>(`/api/admin/payments?${qs}`), [qs.toString()])

  useEffect(() => {
    const t = window.setTimeout(() => {
      if ((params.get('q') || '') !== q) {
        const p = new URLSearchParams(params)
        p.set('q', q)
        p.delete('offset')
        setParams(p, { replace: true })
      }
    }, 350)
    return () => window.clearTimeout(t)
  }, [q, params, setParams])

  const set = (k: string, v: string) => {
    const p = new URLSearchParams(params)
    if (v) p.set(k, v)
    else p.delete(k)
    if (k !== 'offset') p.delete('offset')
    setParams(p)
  }
  const sum = data.data?.items.filter((p) => p.status === 'succeeded').reduce((a, p) => a + p.amount, 0) || 0

  return (
    <>
      <div className="admin-head">
        <h1>Платежи</h1>
        {data.data && (
          <span className="muted small num">
            {data.data.total} шт. · на странице оплачено {rub(sum)}
          </span>
        )}
      </div>
      <div className="filters toolbar">
        <input className="input grow" placeholder="ID ЮKassa, @username, почта, Telegram ID" value={q} onChange={(e) => setQ(e.target.value)} />
        <select className="input" value={params.get('status') || ''} onChange={(e) => set('status', e.target.value)}>
          <option value="">Все статусы</option>
          <option value="succeeded">Оплачен</option>
          <option value="pending">Ожидает</option>
          <option value="canceled">Отменён</option>
          <option value="refunded">Возврат</option>
        </select>
        <label className="input date-field">
          <span>с</span>
          <input type="date" value={params.get('from') || ''} max={params.get('to') || undefined} onChange={(e) => set('from', e.target.value)} aria-label="С даты" />
        </label>
        <label className="input date-field">
          <span>по</span>
          <input type="date" value={params.get('to') || ''} min={params.get('from') || undefined} onChange={(e) => set('to', e.target.value)} aria-label="По дату" />
        </label>
      </div>
      {data.error ? (
        <ErrorBox text={data.error} retry={data.reload} />
      ) : !data.data ? (
        <div className="center" style={{ padding: 40 }}>
          <Spinner />
        </div>
      ) : data.data.items.length === 0 ? (
        <div className="card empty">
          <h3>Платежей не найдено</h3>
        </div>
      ) : (
        <>
          <div className="table-wrap stack-sm">
            <table className="t stack-sm">
              <thead>
                <tr>
                  <th>Дата</th>
                  <th>Клиент</th>
                  <th>Операция</th>
                  <th>Сумма</th>
                  <th>Статус</th>
                  <th>Чек</th>
                </tr>
              </thead>
              <tbody>
                {data.data.items.map((p) => (
                  <tr key={p.id} className="click" {...pressable(() => setOpen(p))}>
                    <td className="num muted wide small nowrap">{dateTime(p.created_at)}</td>
                    <td className="wide lead">
                      <div className="person">
                        <Avatar name={p.customer_name || `#${p.customer_id}`} seed={p.customer_id} size={30} />
                        <span className="cell-title">{p.customer_name || `#${p.customer_id}`}</span>
                      </div>
                    </td>
                    <td className="wide pay-op">
                      {operationName[p.operation] || p.operation}: {p.title}
                      <div className="tiny muted">{p.days} дн. · {p.method || p.provider}</div>
                    </td>
                    <td className="num amount" data-label="Сумма">{rub(p.amount)}</td>
                    <td data-label="Статус">
                      <span className={`sub-state ${p.status === 'succeeded' ? 'active' : p.status === 'pending' ? 'soon' : p.status === 'refunded' ? 'expired' : 'none'}`}>{paymentStatus[p.status]}</span>
                    </td>
                    <td className={p.receipt_status === 'failed' ? 'err' : 'muted'} data-label="Чек">{receiptName[p.receipt_status] || p.receipt_status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pager offset={offset} count={data.data.items.length} total={data.data.total} size={PAGE} onOffset={(o) => set('offset', String(o))} />
        </>
      )}
      <Sheet open={!!open} onClose={() => setOpen(null)} title="Платёж">
        {open && (
          <div className="stack">
            <div className="list small">
              {[
                ['Сумма', rub(open.amount) + (open.discount_percent ? ` (скидка ${open.discount_percent}%, без скидки ${rub(open.base_amount)})` : '')],
                ['Статус', paymentStatus[open.status]],
                ['Операция', `${operationName[open.operation] || open.operation}: ${open.title}, ${open.days} дн.`],
                ['Способ', open.method || '—'],
                ['Создан', dateTime(open.created_at)],
                ['Оплачен', dateTime(open.paid_at)],
                ['Подписка выдана', dateTime(open.applied_at)],
                ['ID ЮKassa', open.provider_ref || '—'],
                ['Наш ID', open.id],
                ['Чек «Мой налог»', `${receiptName[open.receipt_status]}${open.receipt_uuid ? ` · ${open.receipt_uuid}` : ''}`],
              ].map(([k, v]) => (
                <div className="item" key={k}>
                  <span className="muted" style={{ width: 140, flex: 'none' }}>
                    {k}
                  </span>
                  <span style={{ wordBreak: 'break-all' }}>{v}</span>
                </div>
              ))}
              {open.receipt_error && (
                <div className="item">
                  <span className="err">{open.receipt_error}</span>
                </div>
              )}
            </div>
            <button className="btn secondary" onClick={() => nav(`/admin/customers/${open.customer_id}`)}>
              Открыть клиента
            </button>
            {open.provider_ref && (
              <a className="btn ghost" href={`https://yookassa.ru/my/payments?search=${open.provider_ref}`} target="_blank" rel="noreferrer">
                Открыть в ЮKassa
              </a>
            )}
          </div>
        )}
      </Sheet>
    </>
  )
}
