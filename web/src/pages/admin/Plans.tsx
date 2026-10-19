import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { del, get, post, type Plan } from '../../lib/api'
import { periodLabel, rub } from '../../lib/format'
import { ChevronDown, Pencil, Plus, Trash } from '../../ui/icons'
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
        <div className="plan-grid">
          {plans.map((p, i) => {
            const remove = () => {
              if (!confirm(`Удалить тариф «${p.name}»? Если по нему есть подписки или платежи, он будет только скрыт.`)) return
              run(async () => {
                const r = await del<{ archived: boolean }>(`/api/admin/plans/${p.id}`)
                await data.reload()
                return r
              }, 'Готово')
            }
            return (
              <article key={p.id} className={p.is_active ? 'plan-card' : 'plan-card off'}>
                <header className="plan-card-head">
                  <div className="plan-order">
                    <button className="tool-btn" disabled={busy || i === 0} onClick={() => move(i, -1)} aria-label="Выше" title="Выше">
                      <ChevronDown style={{ transform: 'rotate(180deg)' }} />
                    </button>
                    <button className="tool-btn" disabled={busy || i === plans.length - 1} onClick={() => move(i, 1)} aria-label="Ниже" title="Ниже">
                      <ChevronDown />
                    </button>
                  </div>
                  <Link to={`/admin/plans/${p.id}`} className="plan-card-name" title={p.name}>
                    {p.name}
                  </Link>
                  <div className="plan-card-tools">
                    <Link className="tool-btn" to={`/admin/plans/${p.id}`} aria-label="Изменить" title="Изменить">
                      <Pencil />
                    </Link>
                    <button className="tool-btn danger" disabled={busy} onClick={remove} aria-label="Удалить" title="Удалить">
                      <Trash />
                    </button>
                  </div>
                </header>
                <div className="plan-flags">
                  <span className="badge">{p.kind === 'traffic' ? 'трафик' : 'подписка'}</span>
                  {p.is_default && <span className="badge accent">по умолчанию</span>}
                  {p.is_trial && <span className="badge">пробный</span>}
                  {p.is_hidden && <span className="badge">скрытый</span>}
                  {!p.is_active && <span className="badge bad">неактивен</span>}
                  {p.extra_devices_allowed && <span className="badge">+устр.</span>}
                  {p.traffic_topup_enabled && <span className="badge">докупка трафика</span>}
                </div>
                {p.periods.length > 0 ? (
                  <div className="plan-prices">
                    {p.periods.map((per) => (
                      <div key={per.id} className={per.is_best ? 'plan-price best' : 'plan-price'}>
                        <span className="k">{periodLabel(per.unit, per.amount)}</span>
                        <span className="v num">{rub(per.price)}</span>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="small muted" style={{ margin: 0 }}>
                    Сроки и цены не заданы
                  </p>
                )}
                <dl className="plan-specs">
                  <div>
                    <dt>Трафик</dt>
                    <dd>{p.traffic_limit_gb ? `${p.traffic_limit_gb} ГБ` : '∞'}</dd>
                  </div>
                  <div>
                    <dt>Устройства</dt>
                    <dd>{p.device_limit || '∞'}</dd>
                  </div>
                  <div>
                    <dt>Тег</dt>
                    <dd>{p.rw_tag || '—'}</dd>
                  </div>
                  <div>
                    <dt>Сквады</dt>
                    <dd>
                      {p.rw_internal_squads.length}
                      {p.rw_external_squad ? ' + внешний' : ''}
                    </dd>
                  </div>
                </dl>
                {!p.is_default && !p.is_trial && p.is_active && (
                  <button className="plan-default" disabled={busy} onClick={() => run(async () => (await post(`/api/admin/plans/${p.id}/default`), data.reload()), 'Тариф по умолчанию изменён')}>
                    Сделать по умолчанию
                  </button>
                )}
              </article>
            )
          })}
        </div>
      )}
    </>
  )
}
