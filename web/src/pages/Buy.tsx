import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { get, post, type BuyResult, type CatalogPlan, type RedeemResult } from '../lib/api'
import { periodLabel, rub, rubRound } from '../lib/format'
import { Back, Check, Gift, Tag, User } from '../ui/icons'
import { ErrorBox, PageLoader, useAction, useLoad, useToast } from '../ui/kit'
import { MethodPicker, followPayment, usePayMethod } from '../ui/pay'

function limitsLine(p: CatalogPlan) {
  const traffic = p.traffic_limit_gb > 0 ? `${p.traffic_limit_gb} ГБ` : 'Безлимит'
  const dev = p.device_limit > 0 ? `${p.device_limit} устр.` : 'без лимита устройств'
  return `${traffic}, ${dev}`
}

export default function Buy() {
  const toast = useToast()
  const [params] = useSearchParams()
  const nav = useNavigate()
  const subID = Number(params.get('sub')) || 0
  const fixedPlan = Number(params.get('plan')) || 0
  // Gift code (?gift=1) or a subscription paid for another customer (?to=<query>&name=<shown name>).
  const gift = params.get('gift') === '1'
  const to = params.get('to') || ''
  const toName = params.get('name') || to
  const forOther = !!to && !gift
  const catalog = useLoad(() => get<CatalogPlan[]>('/api/plans'))
  const [planID, setPlanID] = useState<number>(fixedPlan)
  const [periodID, setPeriodID] = useState<number>(0)
  const pay = usePayMethod()
  const { busy, run } = useAction()
  const [promoOpen, setPromoOpen] = useState(false)
  const [promo, setPromo] = useState('')

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

  const checkout = () =>
    run(async () => {
      if (gift || forOther) {
        const res = await post<BuyResult>(gift ? '/api/gifts' : '/api/buy-for', {
          recipient: to,
          plan_id: planID,
          period_id: periodID,
          method: pay.method,
        })
        followPayment(res, nav, gift ? '/gifts' : '/')
        return
      }
      const res = await post<BuyResult>('/api/payments', {
        plan_id: planID,
        period_id: periodID,
        subscription_id: subID,
        method: pay.method,
        save_card: pay.save,
      })
      followPayment(res, nav)
    })

  const applyPromo = () =>
    run(async () => {
      const r = await post<RedeemResult>('/api/promo', { code: promo, subscription_id: subID })
      toast(r.message)
      setPromo('')
      setPromoOpen(false)
      await catalog.reload()
    })

  return (
    <div className="stack lg">
      <div className="row">
        <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
          <Back />
        </button>
        <h1>{gift ? 'Подарок' : forOther ? 'Покупка другому' : subID ? 'Продление' : 'Новая подписка'}</h1>
      </div>

      {(gift || forOther) && (
        <div className="card for-banner">
          <span className="action-ico">{gift ? <Gift /> : <User />}</span>
          <div className="grow">
            <b>{gift ? 'Подписка в подарок' : `Для: ${toName}`}</b>
            <div className="tiny muted">
              {gift ? 'После оплаты вы получите код и ссылку — перешлите их получателю.' : 'Оплачиваете вы, подписка появится у получателя.'}
            </div>
          </div>
        </div>
      )}

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
          <div className="periods card" role="radiogroup" aria-label="Срок">
            {plan.offers.map((o) => (
              <button key={o.period_id} role="radio" aria-checked={o.period_id === periodID} className={o.period_id === periodID ? 'period on' : 'period'} onClick={() => setPeriodID(o.period_id)}>
                {o.is_best && <span className="best">Лучший выбор</span>}
                <div className="p-len">{periodLabel(o.unit, o.amount)}</div>
                <div className="p-right num">
                  {o.full_price > o.price && <span className="strike">{rubRound(o.full_price)}</span>}
                  <span className="p-price">{rub(o.price)}</span>
                </div>
                {o.days > 31 && o.per_month > 0 && <span className="p-month num">{rubRound(o.per_month)} / месяц</span>}
              </button>
            ))}
          </div>

          <div className="card stack choice">
            {offer && (
              <>
                <h3>Ваш выбор</h3>
                <div className="spread choice-line">
                  <span>
                    {plan.name} <span className="muted">на {periodLabel(offer.unit, offer.amount)}</span>
                  </span>
                  <b className="num accent-text">{rub(offer.price)}</b>
                </div>
              </>
            )}
            {(plan.description || plan.features.length > 0) && (
              <>
                <h3>Что входит в подписку?</h3>
                <ul className="checks">
                  {[...plan.features, ...(plan.description ? [plan.description] : [])].map((f, i) => (
                    <li key={i}>
                      <Check />
                      <span>{f}</span>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </div>

          <div className="promo-line">
            {plan.promo_code ? (
              <span className="badge accent">
                <Tag /> {plan.promo_code}: −{plan.discount_percent}%
              </span>
            ) : plan.discount_percent > 0 ? (
              <span className="badge accent">Ваша скидка {plan.discount_percent}%</span>
            ) : null}
            {!promoOpen ? (
              <button className="link-btn" onClick={() => setPromoOpen(true)}>
                Есть промокод?
              </button>
            ) : (
              <form
                className="input-row"
                onSubmit={(e) => {
                  e.preventDefault()
                  applyPromo()
                }}
              >
                <input className="input" autoFocus placeholder="Промокод" value={promo} onChange={(e) => setPromo(e.target.value.toUpperCase())} />
                <button className="btn secondary" disabled={busy || !promo.trim()}>
                  Применить
                </button>
              </form>
            )}
          </div>

          {offer && offer.price > 0 && <MethodPicker state={pay} autopay={!gift && !forOther} />}

          {offer && (
            <div className="paybar card">
              <div className="spread">
                <div>
                  {offer.base_price > offer.price && <div className="strike small num">{rub(offer.base_price)}</div>}
                  <div className="row" style={{ gap: 8, alignItems: 'baseline' }}>
                    <span className="small muted">Итого</span>
                    <span className="paybar-sum num">{rub(offer.price)}</span>
                  </div>
                </div>
                <button className="pill-btn paybar-btn" disabled={busy || (offer.price > 0 && !pay.method)} onClick={checkout}>
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
