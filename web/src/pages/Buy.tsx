import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { get, post, type BuyResult, type CatalogPlan } from '../lib/api'
import { useApp } from '../lib/app'
import { periodLabel, rub, rubRound } from '../lib/format'
import { haptic, openLink } from '../lib/tg'
import { Back } from '../ui/icons'
import { ErrorBox, PageLoader, Toggle, useAction, useLoad } from '../ui/kit'

function limitsLine(p: CatalogPlan) {
  const traffic = p.traffic_limit_gb > 0 ? `${p.traffic_limit_gb} ГБ` : 'Безлимит'
  const dev = p.device_limit > 0 ? `${p.device_limit} устр.` : 'без лимита устройств'
  return `${traffic}, ${dev}`
}

export default function Buy() {
  const { config } = useApp()
  const [params] = useSearchParams()
  const nav = useNavigate()
  const subID = Number(params.get('sub')) || 0
  const fixedPlan = Number(params.get('plan')) || 0
  const catalog = useLoad(() => get<CatalogPlan[]>('/api/plans'))
  const [planID, setPlanID] = useState<number>(fixedPlan)
  const [periodID, setPeriodID] = useState<number>(0)
  const [method, setMethod] = useState(config.payment_methods[0]?.code || '')
  const [saveCard, setSaveCard] = useState(true)
  const { busy, run } = useAction()

  const plans = catalog.data || []
  const plan = useMemo(() => plans.find((p) => p.id === planID), [plans, planID])

  useEffect(() => {
    if (!planID && plans.length) setPlanID((plans.find((p) => p.is_default) || plans[0]).id)
  }, [plans, planID])
  useEffect(() => {
    if (plan && !plan.offers.some((o) => o.period_id === periodID)) {
      setPeriodID((plan.offers.find((o) => o.is_best) || plan.offers[0])?.period_id || 0)
    }
  }, [plan, periodID])

  if (catalog.loading && !catalog.data) return <PageLoader />
  if (catalog.error) return <ErrorBox text={catalog.error} retry={catalog.reload} />
  if (!plans.length)
    return (
      <div className="card empty">
        <h3>Тарифов пока нет</h3>
        <p className="small">Загляните чуть позже или напишите в поддержку.</p>
      </div>
    )

  const offer = plan?.offers.find((o) => o.period_id === periodID)

  const pay = () =>
    run(async () => {
      const res = await post<BuyResult>('/api/payments', {
        plan_id: planID,
        period_id: periodID,
        subscription_id: subID,
        method,
        save_card: saveCard,
      })
      if (res.applied) {
        haptic('success')
        nav('/', { replace: true })
        return
      }
      nav(`/payment/${res.payment_id}`)
      if (res.confirmation_url) openLink(res.confirmation_url)
    })

  return (
    <div className="stack lg">
      <div className="row">
        <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
          <Back />
        </button>
        <h1>{subID ? 'Продление' : 'Новая подписка'}</h1>
      </div>

      {!fixedPlan && (
        <div className="stack">
          {plans.map((p) => (
            <button key={p.id} className={p.id === planID ? 'plan-opt on' : 'plan-opt'} onClick={() => setPlanID(p.id)}>
              <div className="spread">
                <span style={{ fontWeight: 600 }}>{p.name}</span>
                <span className="price num">от {rubRound(p.price_per_month)}/мес</span>
              </div>
              <div className="small muted" style={{ marginTop: 4 }}>
                {limitsLine(p)}
              </div>
            </button>
          ))}
        </div>
      )}

      {plan && (
        <>
          {fixedPlan > 0 && (
            <div className="card">
              <div style={{ fontWeight: 600 }}>{plan.name}</div>
              <div className="small muted">{limitsLine(plan)}</div>
            </div>
          )}
          <div className="periods" role="radiogroup" aria-label="Срок">
            {plan.offers.map((o) => (
              <button key={o.period_id} role="radio" aria-checked={o.period_id === periodID} className={o.period_id === periodID ? 'period on' : 'period'} onClick={() => setPeriodID(o.period_id)}>
                {o.is_best && <span className="badge accent best">Выгодно</span>}
                <div className="p-len">{periodLabel(o.unit, o.amount)}</div>
                <div className="p-price num">{rub(o.price)}</div>
                <div className="p-month num">
                  {o.full_price > o.price && <span className="strike">{rubRound(o.full_price)}</span>} {o.days >= 28 ? `${rubRound(o.per_month)}/мес` : ''}
                </div>
              </button>
            ))}
          </div>

          {(plan.description || plan.features.length > 0) && (
            <div className="card stack">
              <h3>Что входит</h3>
              {plan.description && <p className="small muted">{plan.description}</p>}
              {plan.features.length > 0 && (
                <ul className="features small">
                  {plan.features.map((f, i) => (
                    <li key={i}>{f}</li>
                  ))}
                </ul>
              )}
            </div>
          )}

          {offer && offer.price > 0 && (
            <div className="card stack">
              <h3>Способ оплаты</h3>
              {config.payment_methods.length === 0 ? (
                <p className="err">Оплата временно недоступна. Напишите в поддержку.</p>
              ) : (
                <div className="chips" style={{ flexWrap: 'wrap', marginBottom: 0 }}>
                  {config.payment_methods.map((m) => (
                    <button key={m.code} className={m.code === method ? 'chip on' : 'chip'} onClick={() => setMethod(m.code)}>
                      {m.title}
                    </button>
                  ))}
                </div>
              )}
              {config.save_card && method === 'bank_card' && (
                <Toggle checked={saveCard} onChange={setSaveCard} label="Запомнить карту" hint="Для автопродления. Отключить можно в профиле." />
              )}
            </div>
          )}

          {offer && (
            <div className="paybar card">
              <div className="spread">
                <div>
                  <div className="small muted">Итого за {periodLabel(offer.unit, offer.amount)}</div>
                  <div className="row" style={{ gap: 8 }}>
                    <span style={{ fontFamily: 'var(--display)', fontSize: '1.5rem' }} className="num">
                      {rub(offer.price)}
                    </span>
                    {offer.base_price > offer.price && <span className="strike small num">{rub(offer.base_price)}</span>}
                  </div>
                </div>
                <button className="btn" disabled={busy || (offer.price > 0 && !method)} onClick={pay}>
                  {offer.price > 0 ? 'Оплатить' : 'Получить'}
                </button>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}
