import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { get, post, type BuyResult, type Subscription, type TrafficOffer } from '../lib/api'
import { bytes, date, rub } from '../lib/format'
import { Renew, Trend } from '../ui/icons'
import { ErrorBox, Sheet, Spinner, useAction, useLoad } from '../ui/kit'
import { MethodPicker, followPayment, usePayMethod } from '../ui/pay'

/** Buy extra traffic or reset the used traffic of a limited subscription (options come from the plan). */
export function TrafficSheet({ sub, open, onClose }: { sub: Subscription; open: boolean; onClose: () => void }) {
  const nav = useNavigate()
  const offer = useLoad(() => (open ? get<TrafficOffer>(`/api/subscriptions/${sub.id}/traffic`) : Promise.resolve(undefined)), [open, sub.id])
  const [pkg, setPkg] = useState(0)
  const pay = usePayMethod()
  const { busy, run } = useAction()
  const o = offer.data

  const buy = () =>
    run(async () => {
      const r = await post<BuyResult>(`/api/subscriptions/${sub.id}/traffic`, { package: pkg, method: pay.method })
      onClose()
      followPayment(r, nav)
    })
  const reset = () =>
    run(async () => {
      const r = await post<BuyResult>(`/api/subscriptions/${sub.id}/traffic/reset`, { method: pay.method })
      onClose()
      followPayment(r, nav)
    }, o?.reset_price ? undefined : 'Трафик обнулён')

  const needsPay = !!o && ((o.topup && o.packages.length > 0) || (o.can_reset && o.reset_price > 0))

  return (
    <Sheet open={open} onClose={onClose} title="Трафик" icon={<Trend />}>
      {offer.error ? (
        <ErrorBox text={offer.error} retry={offer.reload} />
      ) : !o ? (
        <Spinner />
      ) : (
        <div className="stack">
          <div className="traffic-now">
            <div className="spread small">
              <span className="muted">Израсходовано</span>
              <b className="num">
                {bytes(o.used_bytes)} из {bytes(o.limit_bytes)}
              </b>
            </div>
            <div className="meter">
              <i style={{ width: `${o.limit_bytes ? Math.min(100, (o.used_bytes / o.limit_bytes) * 100) : 0}%` }} />
            </div>
            {o.extra_bytes > 0 && (
              <div className="tiny muted">
                Включая докупленные {bytes(o.extra_bytes)} — действуют до {date(o.extra_until)}
              </div>
            )}
          </div>

          {o.topup && (
            <div className="stack">
              <h3>Докупить трафик</h3>
              <div className="periods card" role="radiogroup" aria-label="Пакет трафика">
                {o.packages.map((p, i) => (
                  <button key={i} role="radio" aria-checked={i === pkg} className={i === pkg ? 'period on' : 'period'} onClick={() => setPkg(i)}>
                    <div className="p-len">+{p.gb} ГБ</div>
                    <div className="p-right num">
                      <span className="p-price">{rub(p.price)}</span>
                    </div>
                  </button>
                ))}
              </div>
              <p className="tiny muted">
                Добавится к лимиту сразу после оплаты и действует до {date(o.until)} — в этот день трафик обнуляется вместе с пакетом.
              </p>
            </div>
          )}

          {needsPay && <MethodPicker state={pay} autopay={false} />}

          {o.topup && (
            <button className="btn block" disabled={busy || !pay.method} onClick={buy}>
              Оплатить {o.packages[pkg] ? rub(o.packages[pkg].price) : ''}
            </button>
          )}

          {o.reset && (
            <div className="card stack">
              <div className="row" style={{ gap: 12 }}>
                <span className="action-ico">
                  <Renew />
                </span>
                <div className="grow" style={{ display: 'grid', gap: 2 }}>
                  <b>Сбросить трафик</b>
                  <span className="tiny muted">
                    {o.can_reset ? 'Обнулим израсходованный трафик, лимит останется прежним.' : `Станет доступно, когда израсходовано ${o.threshold}% трафика.`}
                  </span>
                </div>
              </div>
              <button className="pill-btn" disabled={busy || !o.can_reset || (o.reset_price > 0 && !pay.method)} onClick={reset}>
                {o.reset_price > 0 ? `Сбросить за ${rub(o.reset_price)}` : 'Сбросить бесплатно'}
              </button>
            </div>
          )}
        </div>
      )}
    </Sheet>
  )
}
