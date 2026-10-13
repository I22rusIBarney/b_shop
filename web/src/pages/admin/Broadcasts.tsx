import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, del, get, post, type Broadcast, type Plan } from '../../lib/api'
import { dateTime } from '../../lib/format'
import { Close, Plus, Trash } from '../../ui/icons'
import { ErrorBox, Field, Spinner, Toggle, useAction, useLoad } from '../../ui/kit'
import { safeHTML } from '../News'

const statusLabel: Record<string, [string, string]> = {
  draft: ['черновик', 'badge'],
  scheduled: ['запланирована', 'badge accent'],
  sending: ['отправляется', 'badge warn'],
  done: ['отправлена', 'badge ok'],
  canceled: ['отменена', 'badge bad'],
}

const blank = (): Broadcast => ({
  id: 0,
  title: '',
  body: '',
  image: '',
  buttons: [],
  audience: { segment: 'all' },
  channel: 'telegram',
  show_in_news: true,
  status: 'draft',
  scheduled_at: null,
  total: 0,
  sent: 0,
  failed: 0,
  created_at: '',
  started_at: null,
  finished_at: null,
})

function toLocal(iso: string | null) {
  if (!iso) return ''
  const d = new Date(iso)
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset())
  return d.toISOString().slice(0, 16)
}

export default function Broadcasts() {
  const list = useLoad(() => get<{ items: Broadcast[]; segments: Record<string, string> }>('/api/admin/broadcasts'))
  const plans = useLoad(() => get<Plan[]>('/api/admin/plans'))
  const [params, setParams] = useSearchParams()
  const [b, setB] = useState<Broadcast | null>(() => {
    const seg = params.get('segment')
    return seg ? { ...blank(), audience: { segment: seg } } : null
  })
  useEffect(() => {
    if (params.get('segment')) setParams({}, { replace: true })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const [count, setCount] = useState<number | null>(null)
  const { busy, run } = useAction()
  const file = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const t = setInterval(() => {
      if (list.data?.items.some((x) => x.status === 'sending' || x.status === 'scheduled')) list.reload()
    }, 5000)
    return () => clearInterval(t)
  }, [list])

  useEffect(() => {
    if (!b) return
    setCount(null)
    const t = setTimeout(() => {
      post<{ count: number }>('/api/admin/broadcasts/audience', { audience: b.audience, channel: b.channel })
        .then((r) => setCount(r.count))
        .catch(() => {})
    }, 300)
    return () => clearTimeout(t)
  }, [b?.audience.segment, b?.audience.plan_id, b?.channel]) // eslint-disable-line react-hooks/exhaustive-deps

  if (list.error) return <ErrorBox text={list.error} retry={list.reload} />
  if (!list.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )
  const segments = list.data.segments

  const upload = (f: File | undefined) =>
    run(async () => {
      if (!f || !b) return
      const fd = new FormData()
      fd.append('file', f)
      const r = await api<{ url: string }>('POST', '/api/admin/files', fd)
      setB({ ...b, image: r.url })
    })

  const send = () =>
    run(async () => {
      if (!confirm(b!.scheduled_at ? 'Запланировать рассылку?' : `Отправить ${count ?? ''} получателям сейчас?`)) return
      await post('/api/admin/broadcasts/send', b)
      setB(null)
      await list.reload()
    }, 'Рассылка поставлена в очередь')

  if (b)
    return (
      <div className="stack lg" style={{ maxWidth: 820 }}>
        <div className="admin-head">
          <h1>{b.id ? 'Рассылка' : 'Новая рассылка'}</h1>
          <button className="btn ghost small" onClick={() => setB(null)}>
            К списку
          </button>
        </div>
        <section className="card stack">
          <Field label="Заголовок" hint="Для ленты «Новости» и темы письма. Пусто — первая строка текста">
            <input className="input" value={b.title} onChange={(e) => setB({ ...b, title: e.target.value })} />
          </Field>
          <Field
            label={`Текст (${b.body.length}/4096)`}
            hint="HTML Telegram: <b>, <i>, <u>, <s>, <code>, <a href>, <blockquote>. Переменные: {first_name}, {plan_name}, {days_left}, {referral_link}, {promo:20} — личный промокод на 20%"
          >
            <textarea className="input" style={{ minHeight: 200 }} value={b.body} onChange={(e) => setB({ ...b, body: e.target.value })} />
          </Field>
          {b.body && (
            <div className="card flat small">
              <div className="tiny muted">Предпросмотр</div>
              {b.image && <img src={b.image} alt="" style={{ maxWidth: '100%', borderRadius: 12, margin: '6px 0' }} />}
              <div style={{ whiteSpace: 'pre-wrap' }} dangerouslySetInnerHTML={{ __html: safeHTML(b.body) }} />
            </div>
          )}
          <div className="row wrap">
            <input ref={file} type="file" hidden accept="image/*" onChange={(e) => upload(e.target.files?.[0])} />
            <button className="btn secondary small" disabled={busy} onClick={() => file.current?.click()}>
              {b.image ? 'Заменить картинку' : 'Добавить картинку'}
            </button>
            {b.image && (
              <button className="btn ghost small" onClick={() => setB({ ...b, image: '' })}>
                Убрать картинку
              </button>
            )}
          </div>
          <Field label="Кнопки" hint="Ссылка https://... или путь кабинета: /buy, /referral, /support">
            <div className="stack">
              {b.buttons.map((x, i) => (
                <div className="row" key={i}>
                  <input className="input" placeholder="Текст" value={x.text} onChange={(e) => setB({ ...b, buttons: b.buttons.map((y, j) => (j === i ? { ...y, text: e.target.value } : y)) })} />
                  <input className="input grow" placeholder="/buy" value={x.url} onChange={(e) => setB({ ...b, buttons: b.buttons.map((y, j) => (j === i ? { ...y, url: e.target.value } : y)) })} />
                  <button className="icon-btn" onClick={() => setB({ ...b, buttons: b.buttons.filter((_, j) => j !== i) })} aria-label="Удалить кнопку">
                    <Close />
                  </button>
                </div>
              ))}
              <div>
                <button className="btn secondary small" onClick={() => setB({ ...b, buttons: [...b.buttons, { text: 'Открыть кабинет', url: '/' }] })}>
                  <Plus /> Кнопка
                </button>
              </div>
            </div>
          </Field>
        </section>
        <section className="card stack">
          <div className="form-grid">
            <Field label="Получатели">
              <select className="input" value={b.audience.segment} onChange={(e) => setB({ ...b, audience: { segment: e.target.value, plan_id: b.audience.plan_id } })}>
                {Object.entries(segments).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </select>
            </Field>
            {b.audience.segment === 'plan' && (
              <Field label="Тариф">
                <select className="input" value={b.audience.plan_id || 0} onChange={(e) => setB({ ...b, audience: { ...b.audience, plan_id: Number(e.target.value) } })}>
                  <option value={0}>Выберите</option>
                  {(plans.data || []).map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            <Field label="Канал">
              <select className="input" value={b.channel} onChange={(e) => setB({ ...b, channel: e.target.value as Broadcast['channel'] })}>
                <option value="telegram">Telegram</option>
                <option value="email">Почта</option>
                <option value="all">Telegram, а если нет — почта</option>
              </select>
            </Field>
            <Field label="Отправить" hint="Пусто — сразу">
              <input className="input" type="datetime-local" value={toLocal(b.scheduled_at)} onChange={(e) => setB({ ...b, scheduled_at: e.target.value ? new Date(e.target.value).toISOString() : null })} />
            </Field>
          </div>
          <Toggle checked={b.show_in_news} onChange={(v) => setB({ ...b, show_in_news: v })} label="Показать в ленте «Новости» в кабинете" />
          <p className="small">
            Получателей: <b>{count ?? '…'}</b>
          </p>
          <div className="row wrap" style={{ justifyContent: 'flex-end' }}>
            <button
              className="btn secondary small"
              disabled={busy}
              onClick={() =>
                run(async () => {
                  const saved = await post<Broadcast>('/api/admin/broadcasts/draft', b)
                  setB(saved)
                  await list.reload()
                }, 'Черновик сохранён')
              }
            >
              Сохранить черновик
            </button>
            <button className="btn secondary small" disabled={busy} onClick={() => run(() => post('/api/admin/broadcasts/test', b), 'Тест отправлен вам')}>
              Тест себе
            </button>
            <button className="btn small" disabled={busy || !b.body.trim()} onClick={send}>
              {b.scheduled_at ? 'Запланировать' : 'Отправить'}
            </button>
          </div>
        </section>
      </div>
    )

  return (
    <>
      <div className="admin-head">
        <h1>Рассылки</h1>
        <button className="btn small" onClick={() => setB(blank())}>
          <Plus /> Новая рассылка
        </button>
      </div>
      {list.data.items.length === 0 ? (
        <div className="card empty">
          <h3>Рассылок ещё не было</h3>
          <p className="small">Отправленные рассылки появляются у клиентов в ленте «Новости».</p>
        </div>
      ) : (
        <div className="table-wrap">
          <table className="t">
            <thead>
              <tr>
                <th>Рассылка</th>
                <th>Статус</th>
                <th>Доставлено</th>
                <th>Когда</th>
                <th>В новостях</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.data.items.map((x) => {
                const [label, cls] = statusLabel[x.status]
                const editable = x.status === 'draft' || x.status === 'scheduled'
                return (
                  <tr key={x.id} className={editable ? 'click' : undefined} onClick={() => editable && setB(x)}>
                    <td style={{ maxWidth: 360 }}>
                      <div className="ellipsis">{x.title || '(без заголовка)'}</div>
                      <div className="tiny muted">{segments[x.audience.segment]} · {x.channel === 'telegram' ? 'Telegram' : x.channel === 'email' ? 'почта' : 'Telegram/почта'}</div>
                    </td>
                    <td>
                      <span className={cls}>{label}</span>
                    </td>
                    <td className="num">
                      {x.sent}/{x.total}
                      {x.failed > 0 && <span className="tiny muted"> · ошибок {x.failed}</span>}
                    </td>
                    <td className="muted">{dateTime(x.finished_at || x.scheduled_at || x.created_at)}</td>
                    <td onClick={(e) => e.stopPropagation()}>
                      <Toggle checked={x.show_in_news} onChange={(v) => run(async () => (await post(`/api/admin/broadcasts/${x.id}/news`, { on: v }), await list.reload()))} label="" />
                    </td>
                    <td onClick={(e) => e.stopPropagation()}>
                      {x.status === 'sending' || x.status === 'scheduled' ? (
                        <button className="btn ghost small" disabled={busy} onClick={() => run(async () => (await post(`/api/admin/broadcasts/${x.id}/cancel`), await list.reload()), 'Остановлено')}>
                          Остановить
                        </button>
                      ) : (
                        <button className="icon-btn" disabled={busy} aria-label="Удалить" onClick={() => confirm('Удалить рассылку? Она пропадёт и из новостей.') && run(async () => (await del(`/api/admin/broadcasts/${x.id}`), await list.reload()))}>
                          <Trash />
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
