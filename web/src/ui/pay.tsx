import { useState } from 'react'
import type { NavigateFunction } from 'react-router-dom'
import type { BuyResult } from '../lib/api'
import { useApp } from '../lib/app'
import { haptic, openLink } from '../lib/tg'
import { Check } from './icons'
import { Toggle } from './kit'

export function usePayMethod() {
  const { config } = useApp()
  const [method, setMethod] = useState(config.payment_methods[0]?.code || '')
  const [save, setSave] = useState(true)
  return { method, setMethod, save, setSave }
}

export type PayState = ReturnType<typeof usePayMethod>

/** Payment methods as an inset grouped list with a checkmark, plus the "remember for autopay" switch. */
export function MethodPicker({ state, title = 'Способ оплаты', autopay = true }: { state: PayState; title?: string; autopay?: boolean }) {
  const { config } = useApp()
  const methods = config.payment_methods
  return (
    <section className="group">
      <h3 className="group-title">{title}</h3>
      {methods.length === 0 ? (
        <div className="group-card">
          <p className="group-row err" style={{ margin: 0 }}>
            Оплата временно недоступна. Напишите в поддержку.
          </p>
        </div>
      ) : (
        <div className="group-card" role="radiogroup" aria-label={title}>
          {methods.map((m) => {
            const on = m.code === state.method
            return (
              <button key={m.code} type="button" role="radio" aria-checked={on} className={on ? 'group-row pick on' : 'group-row pick'} onClick={() => state.setMethod(m.code)}>
                <span className="grow">{m.title}</span>
                <span className="checkmark" aria-hidden="true">
                  {on && <Check />}
                </span>
              </button>
            )
          })}
          {autopay && config.save_card && (
            <div className="group-row">
              <Toggle checked={state.save} onChange={state.setSave} label="Автопродление" hint="Сохраним способ оплаты и продлим подписку сами. Отключить можно в профиле" />
            </div>
          )}
        </div>
      )}
    </section>
  )
}

/** After creating a payment: free ones are done, others go to the status page and the payment form. */
export function followPayment(res: BuyResult, nav: NavigateFunction, done = '/') {
  if (res.applied) {
    haptic('success')
    nav(done, { replace: true })
    return
  }
  nav(`/payment/${res.payment_id}`)
  if (res.confirmation_url) openLink(res.confirmation_url)
}
