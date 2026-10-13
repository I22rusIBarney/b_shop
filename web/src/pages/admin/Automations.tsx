import { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, del, get, getToken, post, put, type AutoStep, type Automation, type TriggerInfo } from '../../lib/api'
import { plural } from '../../lib/format'
import { Close, Pencil, Plus, Trash } from '../../ui/icons'
import { ErrorBox, Field, Spinner, Toggle, useAction, useLoad } from '../../ui/kit'

interface Overview {
  items: Automation[]
  warnings: string[]
  triggers: TriggerInfo[]
  enabled: boolean
}

const newStep = (n: number): AutoStep => ({
  step_order: n,
  delay_duration: n === 1 ? '00:00:00' : '1 day',
  template_id: null,
  message_content: '',
  stop_on_purchase: true,
  buttons: [],
  repeat_count: 1,
  repeat_interval: null,
  slots: [],
  webhook_url: null,
  webhook_method: null,
})

const delayPresets = ['00:00:00', '1 hour', '3 hours', '12 hours', '1 day', '2 days', '3 days', '7 days', '14 days']

function delayText(d: string) {
  if (!d || d === '00:00:00') return 'сразу'
  const m = /^(\d+)\s*(day|hour|week|min)/.exec(d)
  if (!m) return d
  const n = Number(m[1])
  return `через ${n} ${{ day: plural(n, 'день', 'дня', 'дней'), hour: plural(n, 'час', 'часа', 'часов'), week: plural(n, 'неделю', 'недели', 'недель'), min: 'мин' }[m[2]]}`
}

export default function Automations() {
  const data = useLoad(() => get<Overview>('/api/admin/automations'))
  const [edit, setEdit] = useState<Automation | null>(null)
  const [importInfo, setImportInfo] = useState<{ created: number; updated: number; warnings: string[] } | null>(null)
  const { busy, run } = useAction()
  const file = useRef<HTMLInputElement>(null)

  if (data.error) return <ErrorBox text={data.error} retry={data.reload} />
  if (!data.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )
  const d = data.data
  const trig = (f: string) => d.triggers.find((t) => t.factor === f)

  const doImport = (f: File | undefined) =>
    run(async () => {
      if (!f) return
      const text = await f.text()
      const res = await api<{ created: number; updated: number; warnings: string[] }>('POST', '/api/admin/automations/import', JSON.parse(text))
      setImportInfo(res)
      await data.reload()
    })

  const doExport = () =>
    run(async () => {
      const res = await fetch('/api/admin/automations/export', { headers: { Authorization: `Bearer ${getToken()}` } })
      if (!res.ok) throw new Error('Не удалось выгрузить')
      const url = URL.createObjectURL(await res.blob())
      const a = document.createElement('a')
      a.href = url
      a.download = `automations-${new Date().toISOString().slice(0, 10)}.json`
      a.click()
      URL.revokeObjectURL(url)
    })

  if (edit) return <Editor a={edit} triggers={d.triggers} all={d.items} onClose={() => setEdit(null)} onSaved={async () => (setEdit(null), await data.reload())} />

  const totals = d.items.reduce((acc, a) => ({ active: acc.active + (a.stats?.active || 0), sent: acc.sent + (a.stats?.sent || 0), runs: acc.runs + (a.stats?.runs || 0), conv: acc.conv + (a.stats?.converted || 0) }), {
    active: 0,
    sent: 0,
    runs: 0,
    conv: 0,
  })

  return (
    <div className="stack lg">
      <div className="admin-head">
        <h1>Автоматизации</h1>
        <div className="row wrap">
          <input ref={file} type="file" hidden accept="application/json,.json" onChange={(e) => doImport(e.target.files?.[0])} />
          <button className="btn secondary small" disabled={busy} onClick={() => file.current?.click()}>
            Импорт
          </button>
          <button className="btn secondary small" disabled={busy} onClick={doExport}>
            Экспорт
          </button>
          <button className="btn small" onClick={() => setEdit({ id: 0, name: '', trigger_type: 'event', trigger_factor: 'abandoned_cart', is_active: false, cooldown_hours: 72, skip_if_active_in: [], schedule_mode: 'relative', loop_count: 1, shuffle_steps: false, steps: [newStep(1)] })}>
            <Plus /> Сценарий
          </button>
        </div>
      </div>

      {!d.enabled && (
        <div className="card flat">
          <b>Автоматизации выключены целиком.</b> Сценарии ниже не запускаются, пока не включите их в <Link to="/admin/settings/automations">настройках</Link> — там же тихие часы и пороги триггеров.
        </div>
      )}

      <div className="stats">
        <div>
          <div className="stat-value">{d.items.filter((a) => a.is_active).length}</div>
          <div className="stat-label">активных сценариев</div>
        </div>
        <div>
          <div className="stat-value">{totals.active}</div>
          <div className="stat-label">в цепочках сейчас</div>
        </div>
        <div>
          <div className="stat-value">{totals.sent}</div>
          <div className="stat-label">отправлено</div>
        </div>
        <div>
          <div className="stat-value">{totals.runs ? Math.round((totals.conv / totals.runs) * 100) : 0}%</div>
          <div className="stat-label">конверсия</div>
        </div>
      </div>

      {importInfo && (
        <div className="card flat small">
          Импортировано: новых {importInfo.created}, обновлено {importInfo.updated}.
          {importInfo.warnings.length === 0 && ' Замечаний нет.'}
        </div>
      )}
      {d.warnings.length > 0 && (
        <div className="card stack" style={{ borderColor: 'color-mix(in srgb, var(--warn) 50%, transparent)' }}>
          <h3>Что стоит поправить</h3>
          <ul className="warn-list small">
            {d.warnings.map((w, i) => (
              <li key={i}>{w}</li>
            ))}
          </ul>
        </div>
      )}

      {d.items.length === 0 ? (
        <div className="card empty">
          <h3>Сценариев нет</h3>
          <p className="small">Импортируйте экспорт из RWP или создайте сценарий вручную.</p>
        </div>
      ) : (
        <div className="table-wrap stack-sm">
          <table className="t stack-sm">
            <thead>
              <tr>
                <th>Сценарий</th>
                <th>Триггер</th>
                <th>Шагов</th>
                <th>В цепочке</th>
                <th>Отправлено</th>
                <th>Конверсия</th>
                <th>Вкл.</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {d.items.map((a) => (
                <tr key={a.id}>
                  <td className="wide">
                    <b className="cell-title">{a.name}</b>
                    <div className="tiny muted">повтор не чаще чем раз в {a.cooldown_hours} ч</div>
                  </td>
                  <td className="wide" data-label="Триггер">
                    {trig(a.trigger_factor)?.title || a.trigger_factor}
                    <div className="tiny muted">{a.trigger_type === 'cron' ? 'по расписанию' : 'по событию'}</div>
                  </td>
                  <td className="num" data-label="Шагов">{a.steps.length}</td>
                  <td className="num" data-label="В цепочке">{a.stats?.active || 0}</td>
                  <td className="num" data-label="Отправлено">{a.stats?.sent || 0}</td>
                  <td className="num" data-label="Конверсия">{a.stats?.runs ? Math.round(((a.stats.converted || 0) / a.stats.runs) * 100) : 0}%</td>
                  <td data-label="Включён">
                    <Toggle checked={a.is_active} onChange={(v) => run(async () => (await post(`/api/admin/automations/${a.id}/active`, { on: v }), await data.reload()))} label="" />
                  </td>
                  <td className="actions">
                    <div className="row" style={{ gap: 4 }}>
                      <button className="icon-btn" onClick={() => setEdit(structuredClone(a))} aria-label="Изменить">
                        <Pencil />
                      </button>
                      <button
                        className="icon-btn"
                        disabled={busy}
                        aria-label="Удалить"
                        onClick={() => confirm(`Удалить «${a.name}»? Активные цепочки остановятся.`) && run(async () => (await del(`/api/admin/automations/${a.id}`), await data.reload()))}
                      >
                        <Trash />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Editor({ a: initial, triggers, all, onClose, onSaved }: { a: Automation; triggers: TriggerInfo[]; all: Automation[]; onClose: () => void; onSaved: () => void }) {
  const [a, setA] = useState(initial)
  const { busy, run } = useAction()
  const t = triggers.find((x) => x.factor === a.trigger_factor)
  const vars = ['first_name', 'plan_name', 'days_left', 'referral_link', 'promo:20', 'promo:days:3']
  const setStep = (i: number, p: Partial<AutoStep>) => setA({ ...a, steps: a.steps.map((s, j) => (j === i ? { ...s, ...p } : s)) })

  const save = () =>
    run(async () => {
      if (a.id) await put(`/api/admin/automations/${a.id}`, a)
      else await post('/api/admin/automations', a)
      onSaved()
    }, 'Сохранено')

  return (
    <div className="stack lg" style={{ maxWidth: 860 }}>
      <div className="admin-head">
        <h1>{a.id ? a.name : 'Новый сценарий'}</h1>
        <button className="btn ghost small" onClick={onClose}>
          К списку
        </button>
      </div>
      <section className="card stack">
        <div className="form-grid">
          <Field label="Название">
            <input className="input" value={a.name} onChange={(e) => setA({ ...a, name: e.target.value })} />
          </Field>
          <Field label="Триггер" hint={t?.about}>
            <select className="input" value={a.trigger_factor} onChange={(e) => setA({ ...a, trigger_factor: e.target.value, trigger_type: triggers.find((x) => x.factor === e.target.value)?.type || 'event' })}>
              {triggers.map((x) => (
                <option key={x.factor} value={x.factor}>
                  {x.title}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Повторный запуск, часов" hint="Не раньше, чем через столько часов после прошлого запуска">
            <input className="input num" type="number" min={0} value={a.cooldown_hours} onChange={(e) => setA({ ...a, cooldown_hours: Number(e.target.value) })} />
          </Field>
          <Field label="Повторить цепочку, раз">
            <input className="input num" type="number" min={1} max={20} value={a.loop_count} onChange={(e) => setA({ ...a, loop_count: Number(e.target.value) })} />
          </Field>
        </div>
        <Field label="Не запускать, если клиент уже в цепочках">
          <div className="row wrap">
            {all
              .filter((x) => x.id !== a.id)
              .map((x) => {
                const on = a.skip_if_active_in.includes(x.name)
                return (
                  <button key={x.id} className={on ? 'chip on' : 'chip'} onClick={() => setA({ ...a, skip_if_active_in: on ? a.skip_if_active_in.filter((n) => n !== x.name) : [...a.skip_if_active_in, x.name] })}>
                    {x.name}
                  </button>
                )
              })}
          </div>
        </Field>
        <Toggle checked={a.shuffle_steps} onChange={(v) => setA({ ...a, shuffle_steps: v })} label="Перемешивать шаги" />
        <Toggle checked={a.is_active} onChange={(v) => setA({ ...a, is_active: v })} label="Сценарий включён" />
      </section>

      {a.steps.map((s, i) => (
        <section key={i} className="card stack step-card">
          <div className="spread">
            <h3>
              Шаг {i + 1} <span className="muted small">· {delayText(s.delay_duration)} {i > 0 ? 'после предыдущего' : 'после запуска'}</span>
            </h3>
            <div className="row" style={{ gap: 4 }}>
              <button className="btn ghost small" disabled={busy} onClick={() => run(() => post('/api/admin/automations/test-step', { step: s, plan_name: 'Бобр' }), 'Отправлено вам')}>
                Тест себе
              </button>
              {a.steps.length > 1 && (
                <button className="icon-btn" onClick={() => setA({ ...a, steps: a.steps.filter((_, j) => j !== i) })} aria-label="Удалить шаг">
                  <Trash />
                </button>
              )}
            </div>
          </div>
          <div className="form-grid">
            <Field label="Задержка" hint="Например: 00:00:00, 3 hours, 2 days, 1 day 12:00:00">
              <input className="input" list="delays" value={s.delay_duration} onChange={(e) => setStep(i, { delay_duration: e.target.value })} />
            </Field>
            <Field label="Повторить шаг, раз">
              <input className="input num" type="number" min={1} max={10} value={s.repeat_count} onChange={(e) => setStep(i, { repeat_count: Number(e.target.value) })} />
            </Field>
            {s.repeat_count > 1 && (
              <Field label="Интервал повторов">
                <input className="input" list="delays" value={s.repeat_interval || ''} onChange={(e) => setStep(i, { repeat_interval: e.target.value || null })} />
              </Field>
            )}
          </div>
          <Field label="Текст" hint={'Переменные: ' + vars.map((v) => `{${v}}`).join(' ')}>
            <textarea className="input" style={{ minHeight: 130 }} value={s.message_content} onChange={(e) => setStep(i, { message_content: e.target.value })} />
          </Field>
          <Field label="Кнопки" hint="Ссылка https://... или путь кабинета: /buy, /referral">
            <div className="stack">
              {s.buttons.map((b, k) => (
                <div className="row" key={k}>
                  <input className="input" placeholder="Текст" value={b.text} onChange={(e) => setStep(i, { buttons: s.buttons.map((x, j) => (j === k ? { ...x, text: e.target.value } : x)) })} />
                  <input className="input grow" placeholder="/buy" value={b.url} onChange={(e) => setStep(i, { buttons: s.buttons.map((x, j) => (j === k ? { ...x, url: e.target.value } : x)) })} />
                  <button className="icon-btn" onClick={() => setStep(i, { buttons: s.buttons.filter((_, j) => j !== k) })} aria-label="Удалить кнопку">
                    <Close />
                  </button>
                </div>
              ))}
              <div>
                <button className="btn secondary small" onClick={() => setStep(i, { buttons: [...s.buttons, { text: 'Выбрать тариф', url: '/buy' }] })}>
                  <Plus /> Кнопка
                </button>
              </div>
            </div>
          </Field>
          <Field label="Вебхук" hint="Необязательно: при отправке шага сюда уйдёт POST с данными клиента">
            <input className="input" placeholder="https://" value={s.webhook_url || ''} onChange={(e) => setStep(i, { webhook_url: e.target.value || null })} />
          </Field>
          <Toggle checked={s.stop_on_purchase} onChange={(v) => setStep(i, { stop_on_purchase: v })} label="Не отправлять, если клиент уже оплатил" hint="Цепочка завершится и засчитается как конверсия" />
        </section>
      ))}
      <datalist id="delays">
        {delayPresets.map((d) => (
          <option key={d} value={d} />
        ))}
      </datalist>

      <div className="row wrap">
        <button className="btn secondary" onClick={() => setA({ ...a, steps: [...a.steps, newStep(a.steps.length + 1)] })}>
          <Plus /> Добавить шаг
        </button>
        <button className="btn" style={{ marginLeft: 'auto' }} disabled={busy} onClick={save}>
          Сохранить
        </button>
      </div>
    </div>
  )
}
