import { useState } from 'react'
import type { NavigateFunction } from 'react-router-dom'
import type { BuyResult } from '../lib/api'
import { useApp } from '../lib/app'
import { haptic, openLink } from '../lib/tg'
import { Toggle } from './kit'

export function usePayMethod() {
  const { config } = useApp()
  const [method, setMethod] = useState(config.payment_methods[0]?.code || '')
  const [save, setSave] = useState(true)
  return { method, setMethod, save, setSave }
}

export type PayState = ReturnType<typeof usePayMethod>

/** Payment method chips plus the "remember for autopay" toggle. */
export function MethodPicker({ state, title = 'Способ оплаты', autopay = true }: { state: PayState; title?: string; autopay?: boolean }) {
  const { config } = useApp()
  return (
    <div className="glass panel stack">
      <h3>{title}</h3>
      {config.payment_methods.length === 0 ? (
        <p className="err">Оплата временно недоступна. Напишите в поддержку.</p>
      ) : config.payment_methods.length > 1 ? (
        <div className="chips" style={{ flexWrap: 'wrap', marginBottom: 0 }}>
          {config.payment_methods.map((m) => (
            <button key={m.code} className={m.code === state.method ? 'chip on' : 'chip'} onClick={() => state.setMethod(m.code)}>
              {m.title}
            </button>
          ))}
        </div>
      ) : (
        <p className="small muted">{config.payment_methods[0].title}</p>
      )}
      {autopay && config.save_card && (
        <Toggle checked={state.save} onChange={state.setSave} label="Включить автопродление" hint="Сохраним способ оплаты и продлим подписку сами. Отключить можно в профиле." />
      )}
    </div>
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
