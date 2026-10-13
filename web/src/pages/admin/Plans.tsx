import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { del, get, post, type Plan } from '../../lib/api'
import { periodLabel, rub } from '../../lib/format'
import { Plus } from '../../ui/icons'
import { ErrorBox, Spinner, Toggle, useAction, useLoad } from '../../ui/kit'

export default function Plans() {
  const data = useLoad(() => get<Plan[]>('/api/admin/plans'))
  const [showInactive, setShowInactive] = useState(false)
  const { busy, run } = useAction()
  const nav = useNavigate()

  if (data.error) return <ErrorBox text={data.error} retry={data.reload} />
  if (!data.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )
  const plans = data.data.filter((p) => showInactive || p.is_active)

  const move = (i: number, dir: -1 | 1) =>
    run(async () => {
      const ids = data.data!.map((p) => p.id)
      const a = ids.indexOf(plans[i].id)
      const b = ids.indexOf(plans[i + dir].id)
      ;[ids[a], ids[b]] = [ids[b], ids[a]]
      await post('/api/admin/plans/reorder', { ids })
      await data.reload()
    })

  return (
    <>
      <div className="admin-head">
        <h1>Тарифы</h1>
        <div className="row">
          <Toggle checked={showInactive} onChange={setShowInactive} label="Показывать неактивные" />
          <button className="btn small" onClick={() => nav('/admin/plans/new')}>
            <Plus /> Новый тариф
          </button>
        </div>
      </div>
      {plans.length === 0 ? (
        <div className="card empty">
          <h3>Тарифов пока нет</h3>
          <p className="small">Создайте первый тариф: название, сроки и цены, лимиты и сквады Remnawave.</p>
        </div>
      ) : (
        <div className="grid2">
          {plans.map((p, i) => (
            <div key={p.id} className="card stack" style={{ opacity: p.is_active ? 1 : 0.6 }}>
              <div className="spread">
                <Link to={`/admin/plans/${p.id}`} style={{ color: 'var(--text)' }}>
                  <h3>{p.name}</h3>
                </Link>
                <div className="row" style={{ gap: 6 }}>
                  {p.is_default && <span className="badge accent">по умолчанию</span>}
                  {p.is_trial && <span className="badge">пробный</span>}
                  {p.is_hidden && <span className="badge">скрытый</span>}
                  {!p.is_active && <span className="badge bad">неактивен</span>}
                </div>
              </div>
              <div className="row wrap small">
                {p.periods.map((per) => (
                  <span key={per.id} className="badge num" style={{ fontWeight: 500 }}>
                    {periodLabel(per.unit, per.amount)} · {rub(per.price)}
                  </span>
                ))}
              </div>
              <div className="small muted">
                Трафик: {p.traffic_limit_gb ? `${p.traffic_limit_gb} ГБ` : '∞'} · Устройств: {p.device_limit || '∞'}
                {p.rw_tag ? ` · Тег ${p.rw_tag}` : ''} · Сквадов: {p.rw_internal_squads.length}
                {p.rw_external_squad ? ' + внешний' : ''}
              </div>
              <div className="row wrap">
                <Link className="btn secondary small" to={`/admin/plans/${p.id}`}>
                  Изменить
                </Link>
                {!p.is_default && !p.is_trial && p.is_active && (
                  <button className="btn ghost small" disabled={busy} onClick={() => run(async () => (await post(`/api/admin/plans/${p.id}/default`), data.reload()), 'Тариф по умолчанию изменён')}>
                    Сделать по умолчанию
                  </button>
                )}
                <button className="btn ghost small" disabled={busy || i === 0} onClick={() => move(i, -1)} aria-label="Выше">
                  ↑
                </button>
                <button className="btn ghost small" disabled={busy || i === plans.length - 1} onClick={() => move(i, 1)} aria-label="Ниже">
                  ↓
                </button>
                <button
                  className="btn danger small"
                  disabled={busy}
                  onClick={() => {
                    if (!confirm(`Удалить тариф «${p.name}»? Если по нему есть подписки или платежи, он будет только скрыт.`)) return
                    run(async () => {
                      const r = await del<{ archived: boolean }>(`/api/admin/plans/${p.id}`)
                      await data.reload()
                      return r
                    }, 'Готово')
                  }}
                >
                  Удалить
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </>
  )
}
