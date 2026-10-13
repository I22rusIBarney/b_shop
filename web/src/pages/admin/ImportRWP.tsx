import { useEffect, useRef, useState } from 'react'
import { get, getToken, post } from '../../lib/api'
import { ErrorBox, Spinner, Toggle, useAction, useLoad } from '../../ui/kit'

interface Report {
  counts: Record<string, number>
  unknown: Record<string, Record<string, number>> | null
  warnings: string[] | null
}
interface Bot {
  id: number
  type: string
  username: string
  customers: number
}
interface State {
  stage: 'idle' | 'uploading' | 'restoring' | 'ready' | 'importing' | 'done' | 'error'
  file_name: string
  size: number
  received: number
  log: string[]
  error: string
  report: Report | null
  bots: Bot[] | null
  bot_id: number
  existing: number
  synced: number
}

const CHUNK = 900 * 1024 // stays under nginx's default 1 MB body limit

const mb = (n: number) => `${(n / 1024 / 1024).toLocaleString('ru-RU', { maximumFractionDigits: 1 })} МБ`

const dumpCmd = `cd ~/remnawave-telegram-shop
DB=$(docker compose ps --services | grep -iE 'db|postgres' | head -1)
docker compose exec -T $DB sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > ~/rwp.dump
ls -lh ~/rwp.dump`

export default function ImportRWP() {
  const load = useLoad(() => get<State>('/api/admin/import/rwp'))
  const [st, setSt] = useState<State | null>(null)
  const [upload, setUpload] = useState<{ sent: number; size: number } | null>(null)
  const [wipe, setWipe] = useState(false)
  const [err, setErr] = useState('')
  const { busy, run } = useAction()
  const file = useRef<HTMLInputElement>(null)
  const logEnd = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (load.data) setSt(load.data)
  }, [load.data])

  const working = st?.stage === 'restoring' || st?.stage === 'importing'
  useEffect(() => {
    if (!working) return
    const t = setInterval(async () => {
      try {
        setSt(await get<State>('/api/admin/import/rwp'))
      } catch {
        /* after a wipe the admin's own session may be gone — keep polling quietly */
      }
    }, 1500)
    return () => clearInterval(t)
  }, [working])
  useEffect(() => logEnd.current?.scrollIntoView({ block: 'nearest' }), [st?.log.length])

  if (load.error) return <ErrorBox text={load.error} retry={load.reload} />
  if (!st)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )

  const send = async (f: File) => {
    setErr('')
    setUpload({ sent: 0, size: f.size })
    try {
      for (let off = 0; off < f.size; off += CHUNK) {
        const part = f.slice(off, Math.min(f.size, off + CHUNK))
        const qs = new URLSearchParams({ name: f.name, size: String(f.size), offset: String(off) })
        const res = await fetch(`/api/admin/import/rwp/chunk?${qs}`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${getToken() || ''}`, 'Content-Type': 'application/octet-stream' },
          body: part,
        })
        const data = await res.json().catch(() => ({}))
        if (!res.ok) throw new Error(data.error || `ошибка ${res.status}`)
        setSt(data as State)
        setUpload({ sent: Math.min(f.size, off + CHUNK), size: f.size })
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setUpload(null)
      if (file.current) file.current.value = ''
    }
  }

  const apply = () =>
    run(async () => {
      if (!confirm(wipe ? 'Удалить текущих клиентов, тарифы и промокоды нового бота и перенести данные из RWP?' : 'Перенести данные из RWP?')) return
      setSt(await post<State>('/api/admin/import/rwp/apply', { wipe }))
    })
  const cancel = () => run(async () => setSt(await post<State>('/api/admin/import/rwp/cancel')))
  const chooseBot = (id: number) => run(async () => setSt(await post<State>('/api/admin/import/rwp/bot', { bot_id: id })))

  const counts = st.report ? Object.entries(st.report.counts).sort(([a], [b]) => a.localeCompare(b, 'ru')) : []
  const unknown = st.report?.unknown ? Object.entries(st.report.unknown) : []

  return (
    <div className="stack lg" style={{ maxWidth: 900 }}>
      <div className="admin-head">
        <h1>Перенос из RWP Shop</h1>
      </div>

      <section className="card stack">
        <p className="small muted" style={{ margin: 0 }}>
          Переносятся клиенты со способами входа, тарифы, подписки, платежи, сохранённые СБП и карты, промокоды (в том числе открывающие скрытые тарифы), рефералы и бонусы, имена устройств,
          FAQ и обращения. Пользователи в панели Remnawave не меняются: ссылки у клиентов остаются прежними. Ключи ЮKassa, «Мой налог», почты и входа через Яндекс/VK вводятся в настройках
          вручную.
        </p>
        <p className="small" style={{ margin: 0 }}>
          <b>1.</b> На сервере старого бота сделайте выгрузку базы (бот можно не останавливать):
        </p>
        <pre className="mono-box" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>
          {dumpCmd}
        </pre>
        <p className="small" style={{ margin: 0 }}>
          <b>2.</b> Скачайте <code>rwp.dump</code> к себе (SFTP, WinSCP, FileZilla или <code>scp</code>) и выберите его здесь. Подойдёт и SQL-файл, и архив .gz / .tar.gz с выгрузкой.
        </p>
        <p className="small" style={{ margin: 0 }}>
          <b>3.</b> Бот проверит файл и покажет, что будет перенесено. Ничего не меняется, пока вы не нажмёте «Перенести».
        </p>
      </section>

      {(st.stage === 'idle' || st.stage === 'error' || st.stage === 'done' || st.stage === 'uploading') && (
        <section className="card stack">
          <h2>Файл базы</h2>
          {upload ? (
            <div className="stack" style={{ gap: 6 }}>
              <div className="hbar-track">
                <div style={{ width: `${(upload.sent / upload.size) * 100}%` }} />
              </div>
              <span className="small muted num">
                Загружено {mb(upload.sent)} из {mb(upload.size)}
              </span>
            </div>
          ) : (
            <div className="row wrap">
              <input ref={file} type="file" accept=".dump,.sql,.gz,.tgz,.tar,.backup,.bak,application/octet-stream" style={{ display: 'none' }} onChange={(e) => e.target.files?.[0] && send(e.target.files[0])} />
              <button className="btn" onClick={() => file.current?.click()}>
                Выбрать файл
              </button>
              {st.stage === 'uploading' && <span className="small muted">Прошлая загрузка прервалась — выберите файл заново</span>}
            </div>
          )}
          {err && <p className="small" style={{ color: 'var(--danger)', margin: 0 }}>{err}</p>}
          {st.stage === 'error' && <p className="small" style={{ color: 'var(--danger)', margin: 0 }}>{st.error}</p>}
          {st.stage === 'done' && (
            <p className="ok-text" style={{ margin: 0 }}>
              ✓ Перенос завершён{st.synced ? `, из панели обновлено подписок: ${st.synced}` : ''}. Проверьте «Статистику» и пару карточек клиентов.
            </p>
          )}
        </section>
      )}

      {(st.stage === 'restoring' || st.stage === 'importing') && (
        <section className="card stack">
          <div className="row">
            <Spinner />
            <b>{st.stage === 'restoring' ? `Проверяю ${st.file_name}…` : 'Переношу данные…'}</b>
          </div>
          <p className="small muted" style={{ margin: 0 }}>
            Можно закрыть страницу — работа продолжится на сервере.
          </p>
        </section>
      )}

      {st.stage === 'ready' && st.report && (
        <section className="card stack">
          <div className="card-title">
            <h2 style={{ margin: 0 }}>Что будет перенесено</h2>
            <span className="muted small">{st.file_name}</span>
          </div>
          {st.bots && st.bots.length > 1 && (
            <div className="stack" style={{ gap: 6 }}>
              <span className="small">В базе несколько ботов. Какой переносить?</span>
              <div className="chips" style={{ flexWrap: 'wrap', marginBottom: 0 }}>
                {st.bots.map((b) => (
                  <button key={b.id} className={st.bot_id === b.id ? 'chip on' : 'chip'} disabled={busy} onClick={() => chooseBot(b.id)}>
                    @{b.username || b.id} · {b.customers} клиентов
                  </button>
                ))}
                <button className={st.bot_id === 0 ? 'chip on' : 'chip'} disabled={busy} onClick={() => chooseBot(0)}>
                  Все вместе
                </button>
              </div>
            </div>
          )}
          <div className="mini-list">
            {counts.map(([k, v]) => (
              <Pair key={k} a={k} b={v.toLocaleString('ru-RU')} />
            ))}
          </div>
          {unknown.length > 0 && (
            <div className="stack" style={{ gap: 4 }}>
              <b className="small">Незнакомые значения (перенесены со значением по умолчанию)</b>
              {unknown.map(([f, m]) => (
                <span key={f} className="small muted">
                  {f}: {Object.entries(m).map(([v, n]) => `«${v}» × ${n}`).join(', ')}
                </span>
              ))}
            </div>
          )}
          {!!st.report.warnings?.length && (
            <div className="stack" style={{ gap: 4 }}>
              <b className="small">Предупреждения</b>
              {st.report.warnings.map((w, i) => (
                <span key={i} className="small muted">
                  • {w}
                </span>
              ))}
            </div>
          )}
          {st.existing > 0 && (
            <div className="stack" style={{ gap: 6, borderTop: '1px solid var(--line)', paddingTop: 12 }}>
              <span className="small">
                В новом боте уже есть клиенты: <b>{st.existing}</b> (например, после тестов). Перенос делается в чистую базу.
              </span>
              <Toggle checked={wipe} onChange={setWipe} label="Заменить текущие данные" hint="Удалятся клиенты с их подписками и платежами, тарифы, промокоды и FAQ. Настройки, оформление, автоматизации и рассылки останутся." />
            </div>
          )}
          {st.error && <p className="small" style={{ color: 'var(--danger)', margin: 0 }}>{st.error}</p>}
          <div className="row wrap">
            <button className="btn" disabled={busy || (st.existing > 0 && !wipe)} onClick={apply}>
              Перенести
            </button>
            <button className="btn ghost" disabled={busy} onClick={cancel}>
              Отменить
            </button>
          </div>
        </section>
      )}

      {st.log.length > 0 && (
        <section className="card stack">
          <h2>Журнал</h2>
          <div className="mono-box" style={{ maxHeight: 260, overflowY: 'auto', whiteSpace: 'pre-wrap', fontSize: '0.8rem' }}>
            {st.log.join('\n')}
            <div ref={logEnd} />
          </div>
        </section>
      )}
    </div>
  )
}

function Pair({ a, b }: { a: string; b: string }) {
  return (
    <>
      <span>{a}</span>
      <span>{b}</span>
    </>
  )
}

