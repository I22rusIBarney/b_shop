import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { get, post, put } from '../../lib/api'
import { dateTime } from '../../lib/format'
import { ErrorBox, Field, Spinner, Toggle, copy, useAction, useLoad, useToast } from '../../ui/kit'

interface Notice {
  enabled: boolean
  template: string
}
interface Settings {
  enabled: boolean
  secret: string
  customer: Record<string, Notice>
  repeat_quiet_hours: number
}
interface EventInfo {
  key: string
  title: string
  hint?: string
  template: string
  vars: string[]
  default: boolean
  button?: string
}
interface Log {
  items: { id: number; event: string; customer_id: number | null; summary: string; result: string; created_at: string }[]
  last: string | null
  url: string
  events: EventInfo[]
}

const MASK = '••••••••'

function randomSecret() {
  const abc = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const b = new Uint8Array(48)
  crypto.getRandomValues(b)
  return Array.from(b, (x) => abc[x % abc.length]).join('')
}

export default function Webhooks() {
  const log = useLoad(() => get<Log>('/api/admin/webhooks'))
  const data = useLoad(() => get<Settings>('/api/admin/settings/webhooks'))
  const [v, setV] = useState<Settings | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [fresh, setFresh] = useState('')
  const { busy, run } = useAction()
  const toast = useToast()

  useEffect(() => {
    if (data.data) setV(structuredClone({ ...data.data, customer: data.data.customer || {} }))
  }, [data.data])

  if (log.error || data.error) return <ErrorBox text={log.error || data.error} retry={() => (log.reload(), data.reload())} />
  if (!v || !log.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )

  const events = log.data.events
  const dirty = JSON.stringify(v) !== JSON.stringify({ ...data.data, customer: data.data?.customer || {} })
  const notice = (e: EventInfo) => v.customer[e.key] || { enabled: e.default, template: '' }
  const setNotice = (e: EventInfo, p: Partial<Notice>) => setV({ ...v, customer: { ...v.customer, [e.key]: { ...notice(e), ...p } } })
  const secret = fresh || (v.secret === MASK ? '' : v.secret)

  const save = () =>
    run(async () => {
      data.setData(await put<Settings>('/api/admin/settings/webhooks', v))
      setFresh('')
    }, 'Сохранено')
  const test = (key: string) =>
    run(async () => {
      if (dirty) await save()
      await post('/api/admin/webhooks/test', { event: key })
    }, 'Пример отправлен вам')
  const envBlock = `WEBHOOK_ENABLED=true\nWEBHOOK_URL=${log.data.url}\nWEBHOOK_SECRET_HEADER=${secret || '<секрет из поля выше>'}`

  return (
    <div className="stack lg" style={{ maxWidth: 900 }}>
      <div className="admin-head">
        <h1>Вебхуки Remnawave</h1>
        <button className="btn small" disabled={busy || !dirty} onClick={save}>
          Сохранить
        </button>
      </div>

      <section className="card stack">
        <p className="small muted" style={{ margin: 0 }}>
          Панель сама сообщает о событиях: подписка закончилась, трафик на исходе, новое устройство, нода упала. Клиентам уходят сообщения ниже, а события нод, входы в панель и торренты — администраторам, по правилам из раздела{' '}
          <Link to="/admin/notifications">«Уведомления»</Link> (группа Remnawave).
        </p>
        <Toggle checked={v.enabled} onChange={(on) => setV({ ...v, enabled: on })} label="Принимать вебхуки" />
        <Field label="Секрет подписи" hint="Только латинские буквы и цифры, от 32 символов. Его же нужно вписать в .env панели.">
          <div className="row" style={{ gap: 8 }}>
            <input
              className="input grow num"
              placeholder={v.secret === MASK ? 'задан — оставьте пустым, чтобы не менять' : 'не задан'}
              value={fresh}
              onChange={(e) => {
                setFresh(e.target.value)
                setV({ ...v, secret: e.target.value || (data.data?.secret ?? '') })
              }}
            />
            <button
              className="btn secondary small"
              onClick={() => {
                const s = randomSecret()
                setFresh(s)
                setV({ ...v, secret: s })
              }}
            >
              Сгенерировать
            </button>
          </div>
        </Field>
        <Field label="Не повторять одно и то же уведомление чаще, чем раз в, часов" hint="Для трафика, окончания подписки и «не подключился»">
          <input className="input num" type="number" min={0} max={168} value={v.repeat_quiet_hours} onChange={(e) => setV({ ...v, repeat_quiet_hours: Number(e.target.value) || 0 })} />
        </Field>
        <Field label="В .env панели Remnawave" hint="После правки .env перезапустите панель: docker compose down && docker compose up -d">
          <pre className="mono-box" style={{ margin: 0, whiteSpace: "pre-wrap" }}>
            {envBlock}
          </pre>
        </Field>
        <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
          <button className="btn ghost small" onClick={async () => toast((await copy(envBlock)) ? 'Скопировано' : 'Не удалось скопировать')}>
            Скопировать
          </button>
          <span className="muted tiny">
            {log.data.last ? `Последний вебхук: ${dateTime(log.data.last)}` : 'Вебхуков от панели ещё не было'}
            {secret && ' · не забудьте сохранить новый секрет'}
          </span>
        </div>
      </section>

      <section className="card stack">
        <h2>Сообщения клиентам</h2>
        {events.map((e) => {
          const n = notice(e)
          return (
            <div key={e.key} className="stack" style={{ borderTop: '1px solid var(--line)', paddingTop: 10 }}>
              <div className="row" style={{ justifyContent: 'space-between', gap: 8, flexWrap: 'wrap' }}>
                <div style={{ flex: '1 1 260px', minWidth: 0 }}>
                  <Toggle checked={n.enabled} onChange={(on) => setNotice(e, { enabled: on })} label={e.title} hint={e.hint} />
                </div>
                <div className="row" style={{ gap: 4, marginLeft: 'auto' }}>
                  <button className="btn ghost small" onClick={() => setOpen(open === e.key ? null : e.key)}>
                    Текст
                  </button>
                  <button className="btn secondary small" disabled={busy} onClick={() => test(e.key)}>
                    Тест
                  </button>
                </div>
              </div>
              {open === e.key && (
                <Field label="Шаблон" hint={'Пусто — стандартный текст. Переменные: ' + e.vars.map((x) => `{${x}}`).join(' ') + (e.button ? ` · кнопка «${e.button}»` : '')}>
                  <textarea className="input" style={{ minHeight: 110 }} placeholder={e.template} value={n.template} onChange={(x) => setNotice(e, { template: x.target.value })} />
                </Field>
              )}
            </div>
          )
        })}
      </section>

      <section className="card stack">
        <div className="card-title">
          <h2 style={{ margin: 0 }}>Журнал</h2>
          <button className="btn ghost small" onClick={log.reload}>
            Обновить
          </button>
        </div>
        {!log.data.items.length ? (
          <p className="muted small">Пусто. Когда панель пришлёт первое событие, оно появится здесь.</p>
        ) : (
          <div className="table-wrap">
            <table className="t">
              <thead>
                <tr>
                  <th>Когда</th>
                  <th>Событие</th>
                  <th>Кто</th>
                  <th>Что сделали</th>
                </tr>
              </thead>
              <tbody>
                {log.data.items.map((x) => (
                  <tr key={x.id}>
                    <td style={{ whiteSpace: "nowrap" }}>{dateTime(x.created_at)}</td>
                    <td className="tiny" style={{ fontFamily: "var(--mono)" }}>{x.event}</td>
                    <td>{x.customer_id ? <Link to={`/admin/customers/${x.customer_id}`}>{x.summary || `#${x.customer_id}`}</Link> : x.summary || '—'}</td>
                    <td>
                      <span className={x.result === 'клиенту' || x.result.endsWith('админам') ? 'badge ok' : x.result.startsWith('ошибка') ? 'badge bad' : 'badge'}>{x.result}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
