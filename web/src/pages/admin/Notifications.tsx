import { useEffect, useState } from 'react'
import { get, post, put, type EventInfo } from '../../lib/api'
import { ErrorBox, Field, Spinner, Toggle, useAction, useLoad } from '../../ui/kit'

interface Route {
  disabled: boolean
  chat_id: number
  topic_id: number
  template: string
}
interface Settings {
  default_chat_id: number
  default_topic_id: number
  events: Record<string, Route>
}

const emptyRoute: Route = { disabled: false, chat_id: 0, topic_id: 0, template: '' }

export default function Notifications() {
  const events = useLoad(() => get<EventInfo[]>('/api/admin/notifications/events'))
  const data = useLoad(() => get<Settings>('/api/admin/settings/notifications'))
  const [v, setV] = useState<Settings | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const { busy, run } = useAction()

  useEffect(() => {
    if (data.data) setV(structuredClone({ ...data.data, events: data.data.events || {} }))
  }, [data.data])

  if (events.error || data.error) return <ErrorBox text={events.error || data.error} retry={() => (events.reload(), data.reload())} />
  if (!v || !events.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )

  const dirty = JSON.stringify(v) !== JSON.stringify({ ...data.data, events: data.data?.events || {} })
  const route = (k: string) => v.events[k] || emptyRoute
  const setRoute = (k: string, p: Partial<Route>) => setV({ ...v, events: { ...v.events, [k]: { ...route(k), ...p } } })
  const groups = Array.from(new Set(events.data.map((e) => e.group)))

  const save = () =>
    run(async () => {
      data.setData(await put<Settings>('/api/admin/settings/notifications', v))
    }, 'Сохранено')

  const test = (key: string) =>
    run(async () => {
      if (dirty) await save()
      await post('/api/admin/notifications/test', { event: key })
    }, 'Пример отправлен')

  return (
    <div className="stack lg" style={{ maxWidth: 900 }}>
      <div className="admin-head">
        <h1>Уведомления администраторам</h1>
        <button className="btn small" disabled={busy || !dirty} onClick={save}>
          Сохранить
        </button>
      </div>
      <section className="card stack">
        <p className="small muted" style={{ margin: 0 }}>
          Куда бот шлёт события. Добавьте бота в группу (для топиков — в группу-форум, с правом писать), узнайте ID чата (вида −100…) и номер топика из ссылки на сообщение в нём. Пустой чат у события — уходит в чат по умолчанию; если и он пуст —
          в личку администраторам из ADMIN_TELEGRAM_IDS. В группах кнопка «Открыть клиента» открывает кабинет в браузере.
        </p>
        <div className="form-grid">
          <Field label="Чат по умолчанию">
            <input className="input num" value={v.default_chat_id || ''} placeholder="-1001234567890" onChange={(e) => setV({ ...v, default_chat_id: Number(e.target.value.replace(/[^\d-]/g, '')) || 0 })} />
          </Field>
          <Field label="Топик по умолчанию">
            <input className="input num" value={v.default_topic_id || ''} placeholder="нет" onChange={(e) => setV({ ...v, default_topic_id: Number(e.target.value) || 0 })} />
          </Field>
        </div>
      </section>

      {groups.map((g) => (
        <section key={g} className="card stack">
          <h2>{g}</h2>
          {events.data!
            .filter((e) => e.group === g)
            .map((e) => {
              const r = route(e.key)
              return (
                <div key={e.key} className="stack" style={{ borderTop: '1px solid var(--line)', paddingTop: 10 }}>
                  <div className="ev-row">
                    <Toggle checked={!r.disabled} onChange={(on) => setRoute(e.key, { disabled: !on })} label={e.title} />
                    <input className="input num" aria-label="Чат" placeholder="по умолч." value={r.chat_id || ''} onChange={(x) => setRoute(e.key, { chat_id: Number(x.target.value.replace(/[^\d-]/g, '')) || 0 })} />
                    <input className="input num" aria-label="Топик" placeholder="топик" value={r.topic_id || ''} onChange={(x) => setRoute(e.key, { topic_id: Number(x.target.value) || 0 })} />
                    <div className="row" style={{ gap: 4 }}>
                      <button className="btn ghost small" onClick={() => setOpen(open === e.key ? null : e.key)}>
                        Текст
                      </button>
                      <button className="btn secondary small" disabled={busy || r.disabled} onClick={() => test(e.key)}>
                        Тест
                      </button>
                    </div>
                  </div>
                  {open === e.key && (
                    <Field label="Шаблон" hint={'Пусто — стандартный текст. Переменные: ' + ['user', 'name', 'username', 'tg_id', 'email', ...e.vars].map((x) => `{${x}}`).join(' ')}>
                      <textarea className="input" style={{ minHeight: 110 }} placeholder={e.template} value={r.template} onChange={(x) => setRoute(e.key, { template: x.target.value })} />
                    </Field>
                  )}
                </div>
              )
            })}
        </section>
      ))}
    </div>
  )
}
