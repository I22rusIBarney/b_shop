import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useApp } from '../lib/app'
import { get, post, type BuyResult, type CatalogPlan, type RedeemResult } from '../lib/api'
import { periodLabel, plural, rub, rubRound } from '../lib/format'
import { Back, Check, Chevron, Gift, Info, Minus, Plus, Tag, User, Warn } from '../ui/icons'
import { ErrorBox, PageLoader, Sheet, useAction, useLoad, useToast } from '../ui/kit'
import { MethodPicker, followPayment, usePayMethod } from '../ui/pay'

function limitsLine(p: CatalogPlan) {
  const traffic = p.traffic_limit_gb > 0 ? `${p.traffic_limit_gb} ГБ` : 'Безлимит'
  const dev = p.device_limit > 0 ? `${p.device_limit} устр.` : 'без лимита устройств'
  return `${traffic}, ${dev}`
}

export default function Buy() {
  const { config, me } = useApp()
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
  const [extra, setExtra] = useState(0)
  const [promoOpen, setPromoOpen] = useState(false)
  const [promo, setPromo] = useState('')
  const [info, setInfo] = useState<CatalogPlan | null>(null)
  const [ack, setAck] = useState(false)

  const plans = catalog.data || []
  const plan = useMemo(() => plans.find((p) => p.id === planID), [plans, planID])

  useEffect(() => {
    if (!planID && plans.length) setPlanID((plans.find((p) => p.is_default) || plans[0]).id)
  }, [plans, planID])
  useEffect(() => setAck(false), [planID])
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
  // Device slider: new subscriptions of plans with extra devices; the price follows the server formula
  // (monthly slot price × days / days in month, then the same discount as the period).
  const slider = !!plan && !subID && !gift && !forOther && plan.extra_devices_allowed && plan.device_limit > 0
  const maxExtra = plan ? (plan.max_extra_devices > 0 ? plan.max_extra_devices : 20) : 0
  const extraN = slider ? Math.min(extra, maxExtra) : 0
  const dim = config.days_in_month || 31
  const extraBase = offer && plan ? Math.floor((plan.extra_device_price * extraN * offer.days) / dim) : 0
  const extraCost = offer && plan ? Math.round(extraBase * (1 - (plan.discount_percent || 0) / 100)) : 0
  const total = offer ? offer.price + extraCost : 0

  const needAck = !!plan?.notice?.trim()
  const canPay = !!offer && !busy && (total === 0 || !!pay.method) && (!needAck || ack)

  const checkout = () =>
    run(async () => {
      if (gift || forOther) {
        const res = await post<BuyResult>(gift ? '/api/gifts' : '/api/buy-for', {
          recipient: to,
          plan_id: planID,
          period_id: periodID,
          method: pay.method,
          notice_ack: ack,
        })
        followPayment(res, nav, gift ? '/gifts' : '/')
        return
      }
      const res = await post<BuyResult>('/api/payments', {
        plan_id: planID,
        period_id: periodID,
        subscription_id: subID,
        extra_devices: extraN,
        method: pay.method,
        save_card: pay.save,
        notice_ack: ack,
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

  const title = gift ? 'Подарок' : forOther ? 'Покупка другому' : subID ? 'Продление' : 'Новая подписка'

  return (
    <div className="buy">
      <header className="large-title">
        <button className="nav-back" onClick={() => nav(-1)} aria-label="Назад">
          <Back />
        </button>
        <h1>{title}</h1>
      </header>

      {/* The invitee bonus comes with the customer's own first purchase, not with gifts. */}
      {me?.invitation && !gift && !forOther && (
        <div className="callout">
          <span className="callout-ico">
            <Gift />
          </span>
          <div>
            <b>
              {me.invitation.from} дарит вам +{me.invitation.days} {plural(me.invitation.days, 'день', 'дня', 'дней')}
            </b>
            <p>Вы пришли по приглашению — добавим дни к подписке сразу после первой оплаты.</p>
          </div>
        </div>
      )}

      {(gift || forOther) && (
        <div className="callout">
          <span className="callout-ico">{gift ? <Gift /> : <User />}</span>
          <div>
            <b>{gift ? 'Подписка в подарок' : `Для: ${toName}`}</b>
            <p>{gift ? 'После оплаты вы получите код и ссылку — перешлите их получателю.' : 'Оплачиваете вы, подписка появится у получателя.'}</p>
          </div>
        </div>
      )}

      {!fixedPlan && (
        <section className="group">
          <h3 className="group-title">Тариф</h3>
          <div className="group-card" role="radiogroup" aria-label="Тариф">
            {plans.map((p) => {
              const on = p.id === planID
              return (
                <div key={p.id} className={on ? 'group-row plan-row on' : 'group-row plan-row'}>
                  <button type="button" role="radio" aria-checked={on} className="plan-row-main" onClick={() => setPlanID(p.id)}>
                    <span className="radio" aria-hidden="true">
                      {on && <Check />}
                    </span>
                    <span className="plan-row-text">
                      <b>{p.name}</b>
                      <span>
                        {limitsLine(p)} · <span className="plan-row-price num">от {rubRound(p.price_per_month)}/мес</span>
                      </span>
                    </span>
                  </button>
                  <button type="button" className="info-btn" onClick={() => setInfo(p)} aria-label={`Подробнее о тарифе ${p.name}`}>
                    <Info />
                  </button>
                </div>
              )
            })}
          </div>
        </section>
      )}

      {plan && (
        <>
          {fixedPlan > 0 && (
            <section className="group">
              <div className="group-card">
                <div className="group-row plan-row">
                  <div className="plan-row-main static">
                    <span className="plan-row-text">
                      <b>{plan.name}</b>
                      <span>{limitsLine(plan)}</span>
                    </span>
                  </div>
                  <button type="button" className="info-btn" onClick={() => setInfo(plan)} aria-label={`Подробнее о тарифе ${plan.name}`}>
                    <Info />
                  </button>
                </div>
              </div>
            </section>
          )}

          <section className="group">
            <h3 className="group-title">Срок</h3>
            <div className="term-grid" role="radiogroup" aria-label="Срок">
              {plan.offers.map((o) => {
                const on = o.period_id === periodID
                return (
                  <button key={o.period_id} type="button" role="radio" aria-checked={on} className={on ? 'term on' : 'term'} onClick={() => setPeriodID(o.period_id)}>
                    <span className="term-len">{periodLabel(o.unit, o.amount)}</span>
                    {o.is_best && <span className="term-best">Выгодно</span>}
                    <span className="term-price num">{rub(o.price)}</span>
                    <span className="term-sub num">
                      {o.full_price > o.price && <s>{rubRound(o.full_price)}</s>}
                      {o.days > 31 && o.per_month > 0 ? `${rubRound(o.per_month)}/мес` : ''}
                    </span>
                  </button>
                )
              })}
            </div>
          </section>

          {slider && (
            <section className="group">
              <div className="group-card">
                <div className="group-row stepper-row">
                  <span className="grow">
                    <b>Устройства</b>
                    <span className="row-sub">
                      {plan.device_limit} в тарифе · каждое следующее +{rub(plan.extra_device_price)}/мес
                    </span>
                  </span>
                  <div className="stepper" role="group" aria-label="Количество устройств">
                    <button type="button" onClick={() => setExtra(Math.max(0, extraN - 1))} disabled={extraN === 0} aria-label="Меньше">
                      <Minus />
                    </button>
                    <output className="num" aria-live="polite">
                      {plan.device_limit + extraN}
                    </output>
                    <button type="button" onClick={() => setExtra(Math.min(maxExtra, extraN + 1))} disabled={extraN >= maxExtra} aria-label="Больше">
                      <Plus />
                    </button>
                  </div>
                </div>
              </div>
            </section>
          )}

          {(plan.description || plan.features.length > 0) && (
            <section className="group">
              <h3 className="group-title">Что входит</h3>
              <div className="group-card">
                {plan.features.map((f, i) => (
                  <div key={i} className="group-row feature-row">
                    <Check />
                    <span>{f}</span>
                  </div>
                ))}
                {plan.description && <p className="group-row group-note">{plan.description}</p>}
              </div>
            </section>
          )}

          {needAck && (
            <section className="group">
              <div className={ack ? 'notice-card ok' : 'notice-card'}>
                <div className="notice-head">
                  <Warn />
                  <b>Важно знать перед покупкой</b>
                </div>
                <p>{plan.notice}</p>
                <label className="notice-ack">
                  <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
                  <span className="box" aria-hidden="true">
                    <Check />
                  </span>
                  Понятно, условия меня устраивают
                </label>
              </div>
            </section>
          )}

          <section className="group">
            <div className="group-card">
              {plan.promo_code || plan.discount_percent > 0 ? (
                <div className="group-row">
                  <Tag />
                  <span className="grow">{plan.promo_code ? `Промокод ${plan.promo_code}` : 'Ваша скидка'}</span>
                  <b className="accent-text num">−{plan.discount_percent}%</b>
                </div>
              ) : null}
              {!promoOpen ? (
                <button type="button" className="group-row link-row" onClick={() => setPromoOpen(true)}>
                  <Tag />
                  <span className="grow">Есть промокод?</span>
                  <Chevron className="chev" />
                </button>
              ) : (
                <form
                  className="group-row promo-form"
                  onSubmit={(e) => {
                    e.preventDefault()
                    applyPromo()
                  }}
                >
                  <input className="plain-input" autoFocus placeholder="Введите промокод" value={promo} onChange={(e) => setPromo(e.target.value.toUpperCase())} aria-label="Промокод" />
                  <button className="text-btn" disabled={busy || !promo.trim()}>
                    Применить
                  </button>
                </form>
              )}
            </div>
          </section>

          {offer && total > 0 && <MethodPicker state={pay} autopay={!gift && !forOther} />}

          {offer && (
            <div className="checkout-bar">
              <div className="checkout-sum">
                <span>{plan.name} · {periodLabel(offer.unit, offer.amount)}</span>
                <b className="num">
                  {offer.base_price > offer.price && <s className="num">{rub(offer.base_price)}</s>}
                  {rub(total)}
                </b>
              </div>
              <button className="cta" disabled={!canPay} onClick={checkout}>
                {total > 0 ? `Оплатить ${rub(total)}` : 'Получить'}
              </button>
              {needAck && !ack && <p className="checkout-hint">Отметьте «Понятно» в блоке «Важно знать»</p>}
            </div>
          )}
        </>
      )}

      <Sheet open={!!info} onClose={() => setInfo(null)} title={info?.name}>
        {info && <PlanInfo p={info} />}
      </Sheet>
    </div>
  )
}

/** Everything about a plan in one sheet: what it is, limits, what is included and what to know before buying. */
function PlanInfo({ p }: { p: CatalogPlan }) {
  return (
    <div className="plan-info">
      {p.description && <p className="lead-text">{p.description}</p>}
      <div className="spec-row">
        <div>
          <span>Трафик</span>
          <b>{p.traffic_limit_gb > 0 ? `${p.traffic_limit_gb} ГБ` : 'Безлимит'}</b>
        </div>
        <div>
          <span>Устройства</span>
          <b>{p.device_limit > 0 ? p.device_limit : '∞'}</b>
        </div>
        <div>
          <span>В месяц от</span>
          <b className="num">{rubRound(p.price_per_month)}</b>
        </div>
      </div>
      {p.features.length > 0 && (
        <ul className="checks">
          {p.features.map((f, i) => (
            <li key={i}>
              <Check />
              <span>{f}</span>
            </li>
          ))}
        </ul>
      )}
      {p.notice?.trim() && (
        <div className="notice-card">
          <div className="notice-head">
            <Warn />
            <b>Важно знать</b>
          </div>
          <p>{p.notice}</p>
        </div>
      )}
    </div>
  )
}
