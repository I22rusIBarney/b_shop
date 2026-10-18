import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, put, type Period, type Plan } from '../../lib/api'
import { periodLabel } from '../../lib/format'
import { Back, Close, Plus, Trash } from '../../ui/icons'
import { ErrorBox, Field, Spinner, Toggle, useAction, useLoad } from '../../ui/kit'

interface Squad {
  uuid: string
  name: string
}

const blank: Plan = {
  id: 0, name: '', kind: 'period', description: '', features: [], sort: 0, is_active: true, is_hidden: false, is_default: false,
  is_trial: false, renewal_disabled: false, change_from_disabled: false, change_to_disabled: false, conversion_disabled: false,
  required_plan_id: null, max_per_customer: 0, traffic_limit_gb: 0, device_limit: 3, extra_devices_allowed: false,
  extra_device_price: 0, max_extra_devices: 0, rw_tag: '', rw_internal_squads: [], rw_external_squad: null,
  traffic_reset_strategy: 'MONTH', reset_traffic_on_buy: false, trial_days: 0,
  traffic_topup_enabled: false, traffic_packages: [], traffic_reset_enabled: false, traffic_reset_price: 0, notice: '',
  periods: [
    { id: 0, unit: 'month', amount: 1, price: 0, is_best: false },
    { id: 0, unit: 'month', amount: 3, price: 0, is_best: true },
    { id: 0, unit: 'month', amount: 6, price: 0, is_best: false },
    { id: 0, unit: 'month', amount: 12, price: 0, is_best: false },
  ],
}

const strategies: Record<string, string> = { NO_RESET: 'Не сбрасывать', DAY: 'Каждый день', WEEK: 'Каждую неделю', MONTH: 'Каждый месяц', MONTH_ROLLING: 'Через месяц от покупки' }

export default function PlanEditor() {
  const { id } = useParams()
  const isNew = id === 'new'
  const nav = useNavigate()
  const plans = useLoad(() => get<Plan[]>('/api/admin/plans'))
  const squads = useLoad(() => get<{ internal: Squad[]; external: Squad[] }>('/api/admin/remnawave/squads'))
  const [p, setP] = useState<Plan | null>(isNew ? structuredClone(blank) : null)
  const [features, setFeatures] = useState('')
  const [newPeriod, setNewPeriod] = useState({ amount: 1, unit: 'month' as Period['unit'] })
  const { busy, run } = useAction()

  useEffect(() => {
    if (!isNew && plans.data) {
      const found = plans.data.find((x) => String(x.id) === id)
      if (found) {
        setP(structuredClone(found))
        setFeatures(found.features.join('\n'))
      }
    }
  }, [plans.data, id, isNew])

  if (plans.error) return <ErrorBox text={plans.error} retry={plans.reload} />
  if (!p)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )

  const set = <K extends keyof Plan>(k: K, v: Plan[K]) => setP({ ...p, [k]: v })
  const setPeriod = (i: number, patch: Partial<Period>) => set('periods', p.periods.map((x, j) => (j === i ? { ...x, ...patch } : patch.is_best ? { ...x, is_best: false } : x)))
  const toggleSquad = (u: string) => set('rw_internal_squads', p.rw_internal_squads.includes(u) ? p.rw_internal_squads.filter((x) => x !== u) : [...p.rw_internal_squads, u])

  const save = () =>
    run(async () => {
      const body = { ...p, features: features.split('\n').map((s) => s.trim()).filter(Boolean) }
      const saved = isNew ? await post<Plan>('/api/admin/plans', body) : await put<Plan>(`/api/admin/plans/${p.id}`, body)
      nav(`/admin/plans/${saved.id}`, { replace: true })
      await plans.reload()
    }, 'Тариф сохранён')

  const others = (plans.data || []).filter((x) => x.id !== p.id)
  return (
    <div className="stack lg" style={{ maxWidth: 980 }}>
      <div className="row">
        <button className="icon-btn" onClick={() => nav('/admin/plans')} aria-label="К тарифам">
          <Back />
        </button>
        <h1>{isNew ? 'Новый тариф' : p.name || 'Тариф'}</h1>
      </div>

      <section className="card stack">
        <h3>Основное</h3>
        <div className="form-grid">
          <Field label="Название">
            <input className="input" value={p.name} onChange={(e) => set('name', e.target.value)} placeholder="Бобрёнок" />
          </Field>
          <Field label="Требуемый тариф" hint="Купить можно только при активной подписке на выбранный">
            <select className="input" value={p.required_plan_id ?? ''} onChange={(e) => set('required_plan_id', e.target.value ? Number(e.target.value) : null)}>
              <option value="">Нет ограничения</option>
              {others.map((x) => (
                <option key={x.id} value={x.id}>
                  {x.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Подписок на клиента" hint="0 — без ограничений">
            <input className="input num" type="number" min={0} value={p.max_per_customer} onChange={(e) => set('max_per_customer', Number(e.target.value))} />
          </Field>
        </div>
        <Field label="Описание">
          <textarea className="input" value={p.description} onChange={(e) => set('description', e.target.value)} placeholder="Для чего этот тариф — коротко и понятно" />
        </Field>
        <Field label="Что входит" hint="Каждый пункт с новой строки">
          <textarea className="input" value={features} onChange={(e) => setFeatures(e.target.value)} placeholder={'Безлимитный трафик\nДо 3 устройств\nYouTube без рекламы'} />
        </Field>
        <Field label="Важно знать перед покупкой" hint="Ограничения тарифа. Если заполнено, клиент перед оплатой отмечает «Понятно» — без этого оплатить нельзя">
          <textarea className="input" value={p.notice} onChange={(e) => set('notice', e.target.value)} maxLength={1000} placeholder="Например: не обходит белые списки мобильных операторов" />
        </Field>
        <div className="form-grid">
          <Toggle checked={p.is_active} onChange={(v) => set('is_active', v)} label="Активен" hint="Неактивный тариф нельзя купить" />
          <Toggle checked={p.is_hidden} onChange={(v) => set('is_hidden', v)} label="Скрытый" hint="Не виден в каталоге, продлевать можно" />
          <Toggle checked={p.is_trial} onChange={(v) => set('is_trial', v)} label="Для пробного периода" hint="Не продаётся, выдаётся как триал" />
          <Toggle checked={p.renewal_disabled} onChange={(v) => set('renewal_disabled', v)} label="Запрет продления" />
          <Toggle checked={p.change_from_disabled} onChange={(v) => set('change_from_disabled', v)} label="Запрет смены с этого тарифа" />
          <Toggle checked={p.change_to_disabled} onChange={(v) => set('change_to_disabled', v)} label="Запрет смены на этот тариф" />
        </div>
      </section>

      <section className="card stack">
        <h3>Сроки и цены</h3>
        {p.periods.length === 0 && <p className="small muted">Добавьте хотя бы один срок.</p>}
        {p.periods.map((per, i) => (
          <div key={i} className="period-edit">
            <span className="pe-label">{periodLabel(per.unit, per.amount)}</span>
            <label className="pe-price">
              <input
                className="input num"
                type="number"
                min={0}
                step="0.01"
                value={per.price / 100}
                onChange={(e) => setPeriod(i, { price: Math.round(Number(e.target.value) * 100) })}
                aria-label={`Цена за ${periodLabel(per.unit, per.amount)}, ₽`}
              />
              <span className="muted">₽</span>
            </label>
            <div className="pe-best">
              <Toggle checked={per.is_best} onChange={(v) => setPeriod(i, { is_best: v })} label="Выгодно" />
            </div>
            <button className="icon-btn pe-del" onClick={() => set('periods', p.periods.filter((_, j) => j !== i))} aria-label={`Удалить срок ${periodLabel(per.unit, per.amount)}`}>
              <Close />
            </button>
          </div>
        ))}
        <div className="row wrap">
          <input className="input num" type="number" min={1} style={{ width: 90 }} value={newPeriod.amount} onChange={(e) => setNewPeriod({ ...newPeriod, amount: Number(e.target.value) })} aria-label="Длительность" />
          <select className="input" style={{ width: 130 }} value={newPeriod.unit} onChange={(e) => setNewPeriod({ ...newPeriod, unit: e.target.value as Period['unit'] })}>
            <option value="day">дней</option>
            <option value="week">недель</option>
            <option value="month">месяцев</option>
          </select>
          <button className="btn secondary small" onClick={() => set('periods', [...p.periods, { id: 0, unit: newPeriod.unit, amount: newPeriod.amount, price: 0, is_best: false }])}>
            <Plus /> Добавить срок
          </button>
        </div>
      </section>

      <section className="card stack">
        <h3>Лимиты</h3>
        <div className="form-grid">
          <Field label="Трафик, ГБ" hint="0 — безлимит">
            <input className="input num" type="number" min={0} value={p.traffic_limit_gb} onChange={(e) => set('traffic_limit_gb', Number(e.target.value))} />
          </Field>
          <Field label="Сброс трафика">
            <select className="input" value={p.traffic_reset_strategy} onChange={(e) => set('traffic_reset_strategy', e.target.value)}>
              {Object.entries(strategies).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Устройств" hint="0 — без ограничения">
            <input className="input num" type="number" min={0} value={p.device_limit} onChange={(e) => set('device_limit', Number(e.target.value))} />
          </Field>
        </div>
        <Toggle checked={p.reset_traffic_on_buy} onChange={(v) => set('reset_traffic_on_buy', v)} label="Сбрасывать трафик при покупке и продлении" />
      </section>

      <section className="card stack">
        <h3>Докупка и сброс трафика</h3>
        {p.traffic_limit_gb > 0 ? (
          <>
            <Toggle
              checked={p.traffic_topup_enabled}
              onChange={(v) => set('traffic_topup_enabled', v)}
              label="Разрешить докупку трафика"
              hint="Пакет действует до ближайшего сброса трафика (при «каждый месяц» — до 1-го числа) и сгорает вместе с израсходованным трафиком; без сброса — до конца оплаченного периода"
            />
            {p.traffic_topup_enabled && (
              <div className="stack">
                {(p.traffic_packages || []).map((pk, i) => (
                  <div key={i} className="row wrap" style={{ gap: 8, alignItems: 'flex-end' }}>
                    <Field label="Объём, ГБ">
                      <input
                        className="input num"
                        type="number"
                        min={1}
                        value={pk.gb}
                        onChange={(e) => set('traffic_packages', p.traffic_packages.map((x, j) => (j === i ? { ...x, gb: Number(e.target.value) } : x)))}
                      />
                    </Field>
                    <Field label="Цена, ₽">
                      <input
                        className="input num"
                        type="number"
                        min={1}
                        value={pk.price / 100}
                        onChange={(e) =>
                          set('traffic_packages', p.traffic_packages.map((x, j) => (j === i ? { ...x, price: Math.round(Number(e.target.value) * 100) } : x)))
                        }
                      />
                    </Field>
                    <button className="icon-btn" aria-label="Удалить пакет" onClick={() => set('traffic_packages', p.traffic_packages.filter((_, j) => j !== i))}>
                      <Trash />
                    </button>
                  </div>
                ))}
                <button className="btn secondary small" style={{ alignSelf: 'flex-start' }} onClick={() => set('traffic_packages', [...(p.traffic_packages || []), { gb: 50, price: 0 }])}>
                  <Plus /> Добавить пакет
                </button>
              </div>
            )}
            <Toggle
              checked={p.traffic_reset_enabled}
              onChange={(v) => set('traffic_reset_enabled', v)}
              label="Разрешить сброс трафика клиентом"
              hint="Кнопка появляется, когда израсходован порог из «Общих» настроек (по умолчанию 80%)"
            />
            {p.traffic_reset_enabled && (
              <Field label="Цена сброса, ₽" hint="0 — бесплатно">
                <input
                  className="input num"
                  type="number"
                  min={0}
                  value={p.traffic_reset_price / 100}
                  onChange={(e) => set('traffic_reset_price', Math.round(Number(e.target.value) * 100))}
                  style={{ maxWidth: 200 }}
                />
              </Field>
            )}
          </>
        ) : (
          <p className="small muted">Доступно для тарифов с лимитом трафика. Сейчас трафик безлимитный.</p>
        )}
      </section>

      <section className="card stack">
        <h3>Remnawave</h3>
        <Field label="Тег" hint="Латиница A–Z, цифры и _, до 16 символов">
          <input className="input" value={p.rw_tag} onChange={(e) => set('rw_tag', e.target.value.toUpperCase())} style={{ maxWidth: 260 }} />
        </Field>
        <div className="field">
          <label>Внутренние сквады</label>
          {squads.error ? (
            <p className="err">Не удалось получить сквады из панели: {squads.error}</p>
          ) : !squads.data ? (
            <Spinner />
          ) : (
            <div className="row wrap">
              {squads.data.internal.map((s) => (
                <button key={s.uuid} className={p.rw_internal_squads.includes(s.uuid) ? 'chip on' : 'chip'} onClick={() => toggleSquad(s.uuid)}>
                  {s.name}
                </button>
              ))}
            </div>
          )}
        </div>
        {squads.data && (
          <Field label="Внешний сквад">
            <select className="input" style={{ maxWidth: 320 }} value={p.rw_external_squad ?? ''} onChange={(e) => set('rw_external_squad', e.target.value || null)}>
              <option value="">Нет</option>
              {squads.data.external.map((s) => (
                <option key={s.uuid} value={s.uuid}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
        )}
      </section>

      <div className="save-bar">
        <button className="btn secondary" onClick={() => nav('/admin/plans')}>
          Отмена
        </button>
        <button className="btn" disabled={busy} onClick={save}>
          {isNew ? 'Создать тариф' : 'Сохранить'}
        </button>
      </div>
    </div>
  )
}
