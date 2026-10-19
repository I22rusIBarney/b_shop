import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { get, post, type Paged, type Payment, type ReconcileItem, type ReconcileReport } from '../../lib/api'
import { dateTime, operationName, paymentStatus, rub } from '../../lib/format'
import { ErrorBox, Field, Sheet, Spinner, useAction, useLoad, pressable } from '../../ui/kit'
import { Avatar, Pager } from '../../ui/admin'

const PAGE = 50
const receiptName: Record<string, string> = { none: '—', pending: 'в очереди', done: 'отправлен', failed: 'ошибка', cancel_pending: 'аннулируется', canceled: 'аннулирован' }

export default function Payments() {
  const [params, setParams] = useSearchParams()
  const nav = useNavigate()
  const [q, setQ] = useState(params.get('q') || '')
  const [open, setOpen] = useState<Payment | null>(null)
  const [recon, setRecon] = useState(false)
  const { busy, run } = useAction()
  const check = (p: Payment) =>
    run(async () => {
      setOpen(await post<Payment>(`/api/admin/payments/${p.id}/check`))
      data.refresh()
    })
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
        <button className="btn small secondary" style={{ marginLeft: 'auto' }} onClick={() => setRecon(true)}>
          Сверка ЮKassa
        </button>
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
            <PayCalc p={open} />
            <div className="list small">
              {[
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
            {open.provider_ref && open.provider === 'yookassa' && (open.status === 'pending' || open.status === 'canceled') && (
              <button className="btn" disabled={busy} onClick={() => check(open)}>
                Проверить в ЮKassa
              </button>
            )}
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
      <Reconcile open={recon} onClose={() => setRecon(false)} onDone={() => data.refresh()} />
    </>
  )
}

// PayCalc shows how the amount was built, in the order the server applies it.
function PayCalc({ p }: { p: Payment }) {
  const afterDiscount = p.amount - (p.commission_amount || 0)
  const rows: [string, string][] = [[p.markup_percent ? `Цена с наценкой +${p.markup_percent}%` : 'Цена', rub(p.base_amount)]]
  if (p.discount_percent) rows.push([`Скидка −${p.discount_percent}%${p.promo_code ? ` (${p.promo_code})` : ''}`, rub(afterDiscount)])
  else if (afterDiscount !== p.base_amount) rows.push(['С округлением', rub(afterDiscount)])
  if (p.commission_amount) rows.push(['Комиссия кассы', '+' + rub(p.commission_amount)])
  return (
    <div className="pay-calc small">
      {rows.map(([k, v]) => (
        <div key={k} style={{ display: 'contents' }}>
          <span className="muted">{k}</span>
          <span className="num">{v}</span>
        </div>
      ))}
      <span className="total">Итого</span>
      <span className="total num">{rub(p.amount)}</span>
    </div>
  )
}

const isoDay = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

function Reconcile({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const nav = useNavigate()
  const [from, setFrom] = useState(() => isoDay(new Date(Date.now() - 6 * 86400e3)))
  const [to, setTo] = useState(() => isoDay(new Date()))
  const [rep, setRep] = useState<ReconcileReport | null>(null)
  const [fail, setFail] = useState('')
  const [busy, setBusy] = useState(false)
  const start = async () => {
    setBusy(true)
    setFail('')
    setRep(null)
    try {
      setRep(await post<ReconcileReport>('/api/admin/payments/reconcile', { from, to }))
      onDone()
    } catch (e: any) {
      setFail(e?.message || 'Сверка не удалась')
    } finally {
      setBusy(false)
    }
  }
  const list = (title: string, items: ReconcileItem[], tone?: string) =>
    items.length > 0 && (
      <div className="stack" key={title}>
        <b className={tone}>{title}: {items.length}</b>
        <div className="recon-list small">
          {items.map((it) => (
            <div className="item" key={it.provider_id}>
              <span>
                {it.customer_id ? (
                  <a href={`/admin/customers/${it.customer_id}`} onClick={(e) => (e.preventDefault(), nav(`/admin/customers/${it.customer_id}`))}>
                    клиент #{it.customer_id}
                  </a>
                ) : (
                  <span className="muted">{it.provider_id}</span>
                )}
                <div className="tiny muted">{it.note}</div>
              </span>
              <span className="num">{rub(it.amount)}</span>
            </div>
          ))}
        </div>
      </div>
    )
  return (
    <Sheet open={open} onClose={onClose} title="Сверка ЮKassa">
      <div className="stack">
        <p className="muted small" style={{ margin: 0 }}>
          Ищет в ЮKassa успешные оплаты за период и выдаёт покупки, которые магазин не обработал (потерянное уведомление, оплата после
          отмены заказа). Уже выданные и возвращённые не трогает — запускать повторно безопасно. Сама сверка идёт каждый час за двое суток.
        </p>
        <div className="form-grid" style={{ gridTemplateColumns: '1fr 1fr' }}>
          <Field label="С">
            <input className="input" type="date" value={from} max={to} onChange={(e) => setFrom(e.target.value)} />
          </Field>
          <Field label="По">
            <input className="input" type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} />
          </Field>
        </div>
        <button className="btn" disabled={busy || !from || !to} onClick={start}>
          {busy ? 'Сверяю…' : 'Запустить сверку'}
        </button>
        {fail && <div className="err">{fail}</div>}
        {rep && (
          <div className="stack">
            <div className="muted small">
              Успешных оплат в ЮKassa: <b className="num">{rep.checked}</b>
              {rep.recovered.length + rep.problems.length + rep.unknown.length === 0 && ' — расхождений нет'}
            </div>
            {list('Выдано сейчас', rep.recovered, 'ok-text')}
            {list('Требуют внимания', rep.problems, 'err')}
            {list('Нет заказа в магазине', rep.unknown)}
          </div>
        )}
      </div>
    </Sheet>
  )
}
