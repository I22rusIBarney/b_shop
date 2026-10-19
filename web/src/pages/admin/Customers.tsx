import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { get, type CustomerRow, type Paged, type Plan } from '../../lib/api'
import { relative, rub, shortDate } from '../../lib/format'
import { ErrorBox, Spinner, useLoad, pressable } from '../../ui/kit'
import PanelSync from './PanelSync'
import { Avatar } from '../../ui/admin'

const PAGE = 50

function name(c: CustomerRow) {
  return [c.first_name, c.last_name].filter(Boolean).join(' ') || (c.username ? `@${c.username}` : '') || c.email || `#${c.id}`
}

/** Subscription state in one glance: coloured pill plus the date it ends or ended. */
function SubState({ c }: { c: CustomerRow }) {
  if (!c.expires_at) return <span className="sub-state none">нет подписки</span>
  const left = Math.ceil((new Date(c.expires_at).getTime() - Date.now()) / 86400000)
  const cls = left <= 0 ? 'expired' : left <= 3 ? 'soon' : 'active'
  const label = left <= 0 ? 'истекла' : left <= 3 ? `${left} дн.` : 'активна'
  return (
    <span className="sub-state-wrap">
      <span className={`sub-state ${cls}`}>{label}</span>
      <span className="sub-state-date num">{shortDate(c.expires_at)}</span>
    </span>
  )
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
      <div className="filters toolbar">
        <input
          className="input grow"
          type="search"
          placeholder="Имя, @username, почта, Telegram ID, ссылка подписки"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label="Поиск клиентов"
        />
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
          <div className="table-wrap stack-sm">
            <table className="t stack-sm cust-table">
              <thead>
                <tr>
                  <th>Клиент</th>
                  <th>Подписка</th>
                  <th>Оплачено</th>
                  <th>Рефералы</th>
                  <th>Активность</th>
                  <th>Создан</th>
                </tr>
              </thead>
              <tbody>
                {data.data.items.map((c) => (
                  <tr key={c.id} className="click" {...pressable(() => nav(`/admin/customers/${c.id}`))}>
                    <td className="wide">
                      <div className="person">
                        <Avatar name={name(c)} seed={c.id} size={36} />
                        <div className="person-text">
                          <div className="person-name">
                            <span className="cell-title">{name(c)}</span>
                            {c.is_admin && <span className="badge accent">админ</span>}
                            {c.bot_blocked && <span className="badge warn">бот заблокирован</span>}
                            {c.is_blocked && <span className="badge bad">заблокирован</span>}
                            {c.tag && <span className="badge">{c.tag}</span>}
                          </div>
                          <div className="person-sub num">{[c.telegram_id, c.username && `@${c.username}`, c.email].filter(Boolean).join(' · ') || '—'}</div>
                        </div>
                      </div>
                    </td>
                    <td className="sub-cell" data-label="Подписка">
                      <SubState c={c} />
                    </td>
                    <td className="num" data-label="Оплачено">
                      {rub(c.paid_total)} <span className="tiny muted">({c.payments_count})</span>
                    </td>
                    <td className="num" data-label="Рефералы">{c.ref_count || <span className="muted">—</span>}</td>
                    <td className="muted" data-label="Активность">{relative(c.last_seen_at)}</td>
                    <td className="muted num" data-label="Создан">{shortDate(c.created_at)}</td>
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
