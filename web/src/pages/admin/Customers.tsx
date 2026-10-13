import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { get, type CustomerRow, type Paged, type Plan } from '../../lib/api'
import { relative, rub, shortDate } from '../../lib/format'
import { ErrorBox, Spinner, useLoad } from '../../ui/kit'
import PanelSync from './PanelSync'

const PAGE = 50

function name(c: CustomerRow) {
  return [c.first_name, c.last_name].filter(Boolean).join(' ') || (c.username ? `@${c.username}` : '') || c.email || `#${c.id}`
}

export default function Customers() {
  const [params, setParams] = useSearchParams()
  const nav = useNavigate()
  const [q, setQ] = useState(params.get('q') || '')
  const status = params.get('status') || ''
  const planID = params.get('plan_id') || ''
  const sort = params.get('sort') || ''
  const offset = Number(params.get('offset')) || 0
  const plans = useLoad(() => get<Plan[]>('/api/admin/plans'))
  const data = useLoad(
    () => get<Paged<CustomerRow>>(`/api/admin/customers?${new URLSearchParams({ q: params.get('q') || '', status, plan_id: planID, sort, limit: String(PAGE), offset: String(offset) })}`),
    [params.toString()],
  )

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

  return (
    <>
      <div className="admin-head">
        <h1>Клиенты</h1>
        <div className="row" style={{ gap: 10 }}>
          {data.data && <span className="muted num">{data.data.total.toLocaleString('ru-RU')}</span>}
          <PanelSync onDone={data.reload} />
        </div>
      </div>
      <div className="filters">
        <input className="input grow" placeholder="Имя, @username, почта, Telegram ID, ссылка подписки" value={q} onChange={(e) => setQ(e.target.value)} />
        <select className="input" value={status} onChange={(e) => set('status', e.target.value)}>
          <option value="">Все</option>
          <option value="active">С активной подпиской</option>
          <option value="trial">На пробном</option>
          <option value="expired">Подписка истекла</option>
          <option value="none">Без подписки</option>
          <option value="bot_blocked">Заблокировали бота</option>
          <option value="blocked">Заблокированы</option>
        </select>
        <select className="input" value={planID} onChange={(e) => set('plan_id', e.target.value)}>
          <option value="">Любой тариф</option>
          {plans.data?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <select className="input" value={sort} onChange={(e) => set('sort', e.target.value)}>
          <option value="">Сначала новые</option>
          <option value="last_seen">По активности</option>
          <option value="expires">Скоро истекают</option>
          <option value="paid">Больше всех заплатили</option>
        </select>
      </div>
      {data.error ? (
        <ErrorBox text={data.error} retry={data.reload} />
      ) : !data.data ? (
        <div className="center" style={{ padding: 40 }}>
          <Spinner />
        </div>
      ) : data.data.items.length === 0 ? (
        <div className="card empty">
          <h3>Никого не нашли</h3>
          <p className="small">Измените фильтры или строку поиска.</p>
        </div>
      ) : (
        <>
          <div className="table-wrap">
            <table className="t">
              <thead>
                <tr>
                  <th>Клиент</th>
                  <th>Подписка до</th>
                  <th>Оплачено</th>
                  <th>Рефералы</th>
                  <th>Активность</th>
                  <th>Создан</th>
                </tr>
              </thead>
              <tbody>
                {data.data.items.map((c) => (
                  <tr key={c.id} className="click" onClick={() => nav(`/admin/customers/${c.id}`)}>
                    <td>
                      <div className="row" style={{ gap: 8 }}>
                        <span>{name(c)}</span>
                        {c.is_admin && <span className="badge accent">админ</span>}
                        {c.bot_blocked && <span className="badge warn">бот заблокирован</span>}
                        {c.is_blocked && <span className="badge bad">заблокирован</span>}
                        {c.tag && <span className="badge">{c.tag}</span>}
                      </div>
                      <div className="tiny muted">{[c.telegram_id, c.username && `@${c.username}`, c.email].filter(Boolean).join(' · ')}</div>
                    </td>
                    <td className="num">
                      {c.expires_at ? <span style={{ color: new Date(c.expires_at) < new Date() ? 'var(--danger)' : undefined }}>{shortDate(c.expires_at)}</span> : <span className="muted">—</span>}
                    </td>
                    <td className="num">
                      {rub(c.paid_total)} <span className="tiny muted">({c.payments_count})</span>
                    </td>
                    <td className="num">{c.ref_count || <span className="muted">—</span>}</td>
                    <td className="muted">{relative(c.last_seen_at)}</td>
                    <td className="muted num">{shortDate(c.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="pager">
            <span className="small muted num">
              {offset + 1}–{offset + data.data.items.length} из {data.data.total}
            </span>
            <button className="btn secondary small" disabled={offset === 0} onClick={() => set('offset', String(Math.max(0, offset - PAGE)))}>
              Назад
            </button>
            <button className="btn secondary small" disabled={offset + PAGE >= data.data.total} onClick={() => set('offset', String(offset + PAGE))}>
              Дальше
            </button>
          </div>
        </>
      )}
    </>
  )
}
