import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, type BuyResult, type ChangeInfo, type ChangeQuote, type ChangeTarget } from '../lib/api'
import { plural, rub, rubRound } from '../lib/format'
import { Back, Help } from '../ui/icons'
import { ErrorBox, PageLoader, useAction, useLoad } from '../ui/kit'
import { MethodPicker, followPayment, usePayMethod } from '../ui/pay'

type Mode = ChangeQuote['mode']

const modes: { mode: Mode; label: string; help: string }[] = [
  { mode: 'upgrade', label: 'Доплата', help: 'Платите только разницу в цене за оставшиеся дни. Срок подписки не меняется.' },
  { mode: 'convert', label: 'Конвертация', help: 'Без оплаты: оставшиеся дни пересчитываются по цене нового тарифа, срок станет короче.' },
  { mode: 'downgrade', label: 'Понижение', help: 'Переход на тариф дешевле: оставшиеся дни пересчитываются в более длинный срок.' },
]

const days = (n: number) => `${n} ${plural(n, 'день', 'дня', 'дней')}`

function limits(t: { traffic_limit_gb: number; device_limit: number }) {
  return [t.traffic_limit_gb > 0 ? `${t.traffic_limit_gb} ГБ` : 'Безлимит', t.device_limit > 0 ? `${t.device_limit} устр.` : 'устройства без лимита']
}

export default function ChangePlan() {
  const { id } = useParams()
  const nav = useNavigate()
  const info = useLoad(() => get<ChangeInfo>(`/api/subscriptions/${id}/change`), [id])
  const [mode, setMode] = useState<Mode>('upgrade')
  const [pick, setPick] = useState<number>(0)
  const [help, setHelp] = useState(false)
  const pay = usePayMethod()
  const { busy, run } = useAction()

  const quote = (t: ChangeTarget) => t.quotes?.find((q) => q.mode === mode)
  const counts = useMemo(() => {
    const c: Record<Mode, number> = { upgrade: 0, convert: 0, downgrade: 0 }
    for (const t of info.data?.targets || []) for (const q of t.quotes || []) c[q.mode]++
    return c
  }, [info.data])

  // Open the first mode that has options.
  useEffect(() => {
    if (!info.data) return
    if (!counts[mode]) {
      const first = modes.find((m) => counts[m.mode] > 0)
      if (first) setMode(first.mode)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info.data])

  if (info.loading && !info.data) return <PageLoader />
  if (info.error) return <ErrorBox text={info.error} retry={info.reload} />
  const d = info.data!
  const list = d.targets.filter((t) => quote(t))
  const blocked = d.targets.filter((t) => t.unavailable)
  const sel = list.find((t) => t.plan_id === pick)
  const q = sel ? quote(sel) : undefined
  const modeInfo = modes.find((m) => m.mode === mode)!

  const submit = () =>
    run(async () => {
      const res = await post<BuyResult>(`/api/subscriptions/${id}/change`, {
        plan_id: pick,
        mode,
        method: pay.method,
        save_card: pay.save,
      })
      followPayment(res, nav)
    }, mode === 'upgrade' ? undefined : 'Тариф изменён')

  return (
    <div className="stack lg change-page">
      <div className="row">
        <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
          <Back />
        </button>
        <h1>Смена тарифа</h1>
      </div>

      <div className="glass panel stack">
        <div className="tiny muted">Текущий тариф</div>
        <div className="cur-plan">{d.plan_name}</div>
        <div className="spec-row">
          {limits(d).map((x) => (
            <span key={x} className="spec">
              {x}
            </span>
          ))}
          <span className="spec">{rubRound(d.price_per_month)}/мес</span>
          <span className="spec accent">осталось {days(d.days_left)}</span>
        </div>
      </div>

      <div className="seg seg-fill" role="tablist" aria-label="Способ смены">
        {modes.map((m) => (
          <button
            key={m.mode}
            role="tab"
            aria-selected={mode === m.mode}
            className={mode === m.mode ? 'on' : ''}
            disabled={!counts[m.mode]}
            onClick={() => {
              setMode(m.mode)
              setPick(0)
            }}
          >
            {m.label}
          </button>
        ))}
      </div>
      <div className="mode-help">
        <button className="link-btn" onClick={() => setHelp(!help)} aria-expanded={help}>
          <Help /> Как это работает
        </button>
        {d.discount_percent > 0 && <span className="badge accent">Ваша скидка {d.discount_percent}%</span>}
      </div>
      {help && <p className="small muted">{modeInfo.help}</p>}
      {d.penalty > 0 && (
        <p className="small muted">Комиссия за смену тарифа — {d.penalty}% от стоимости оставшихся дней, она уже учтена в расчёте.</p>
      )}
      {d.blocked && <div className="glass panel err">{d.blocked}</div>}

      {list.length === 0 ? (
        <div className="glass panel empty">Для этого способа подходящих тарифов нет</div>
      ) : (
        <div className="stack">
          {list.map((t) => {
            const tq = quote(t)!
            const on = t.plan_id === pick
            return (
              <button key={t.plan_id} className={on ? 'plan-opt glass on' : 'plan-opt glass'} onClick={() => setPick(t.plan_id)} aria-pressed={on}>
                <div className="spread">
                  <span className="po-name">{t.name}</span>
                  {tq.mode === 'upgrade' ? <span className="price num">+{rub(tq.amount)}</span> : <span className="price num">{tq.days_before} → {tq.days_after} дн.</span>}
                </div>
                <div className="small muted" style={{ marginTop: 4 }}>
                  {[...limits(t), `${rubRound(t.price_per_month)}/мес`].join(' · ')}
                </div>
                {on && t.features?.length > 0 && (
                  <ul className="features small" style={{ marginTop: 8 }}>
                    {t.features.map((f, i) => (
                      <li key={i}>{f}</li>
                    ))}
                  </ul>
                )}
              </button>
            )
          })}
        </div>
      )}

      {blocked.length > 0 && (
        <div className="stack">
          {blocked.map((t) => (
            <div key={t.plan_id} className="plan-opt glass off">
              <div className="spread">
                <span className="po-name">{t.name}</span>
                <span className="tiny muted">{t.unavailable}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {sel && q?.mode === 'upgrade' && <MethodPicker state={pay} />}

      {sel && q && (
        <div className="paybar glass panel">
          <div className="spread">
            <div style={{ minWidth: 0 }}>
              <div className="small muted ellipsis">
                {d.plan_name} → {sel.name}
              </div>
              {q.mode === 'upgrade' ? (
                <div className="row" style={{ gap: 8 }}>
                  <span className="pay-total num">{rub(q.amount)}</span>
                  {q.base_amount > q.amount && <span className="strike small num">{rub(q.base_amount)}</span>}
                </div>
              ) : (
                <div className="pay-total">{days(q.days_after)}</div>
              )}
            </div>
            <button className="btn" disabled={busy || !!d.blocked || (q.mode === 'upgrade' && !pay.method)} onClick={submit}>
              {q.mode === 'upgrade' ? 'Оплатить' : 'Перейти'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
