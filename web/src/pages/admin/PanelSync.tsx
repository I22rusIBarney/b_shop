import { useEffect, useState } from 'react'
import { get, post } from '../../lib/api'
import { Renew } from '../../ui/icons'
import { Sheet, Spinner, useAction } from '../../ui/kit'

interface SyncState {
  running: boolean
  started_at: string | null
  finished_at: string | null
  total: number
  done: number
  updated: number
  attached: number
  created: number
  skipped: number
  errors: string[]
  error: string
}

/** Button + sheet: pull all users from the Remnawave panel into the shop. */
export default function PanelSync({ onDone }: { onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [st, setSt] = useState<SyncState | null>(null)
  const { busy, run } = useAction()

  useEffect(() => {
    if (!open) return
    get<SyncState>('/api/admin/panel-sync').then(setSt).catch(() => {})
  }, [open])
  useEffect(() => {
    if (!st?.running) return
    const t = setInterval(async () => {
      const s = await get<SyncState>('/api/admin/panel-sync').catch(() => null)
      if (!s) return
      setSt(s)
      if (!s.running) onDone()
    }, 1200)
    return () => clearInterval(t)
  }, [st?.running, onDone])

  const start = () => run(async () => setSt(await post<SyncState>('/api/admin/panel-sync')))
  const pct = st && st.total ? Math.round((st.done / st.total) * 100) : 0

  return (
    <>
      <button className="btn secondary small" onClick={() => setOpen(true)}>
        <Renew /> Синхронизировать с панелью
      </button>
      <Sheet open={open} onClose={() => setOpen(false)} title="Синхронизация с панелью" icon={<Renew />}>
        <div className="stack">
          <p className="small muted" style={{ margin: 0 }}>
            Бот возьмёт всех пользователей из панели Remnawave. Уже известные подписки обновятся (срок, трафик, ссылка). Новых пользователей бот привяжет к клиенту с тем же
            Telegram ID или почтой, а если такого нет — создаст клиента. Тариф определяется по тегу пользователя в панели. Пользователи без Telegram ID и почты пропускаются:
            их не с кем связать.
          </p>
          {st?.running ? (
            <div className="stack" style={{ gap: 6 }}>
              <div className="row">
                <Spinner />
                <span className="small">
                  {st.done} из {st.total || '…'}
                </span>
              </div>
              <div className="hbar-track">
                <div style={{ width: `${pct}%` }} />
              </div>
            </div>
          ) : (
            <button className="btn" disabled={busy} onClick={start}>
              {st?.finished_at ? 'Запустить ещё раз' : 'Запустить'}
            </button>
          )}
          {st && (st.finished_at || st.running) && (
            <div className="mini-list">
              <span>Пользователей в панели</span>
              <span>{st.total}</span>
              <span>Обновлено подписок</span>
              <span>{st.updated}</span>
              <span>Привязано к клиентам</span>
              <span>{st.attached}</span>
              <span>Новых клиентов</span>
              <span>{st.created}</span>
              <span>Пропущено (нет Telegram ID и почты)</span>
              <span>{st.skipped}</span>
            </div>
          )}
          {st?.error && <p className="small" style={{ color: 'var(--danger)', margin: 0 }}>{st.error}</p>}
          {!!st?.errors?.length && (
            <div className="mono-box small" style={{ maxHeight: 160, overflowY: 'auto' }}>
              {st.errors.join('\n')}
            </div>
          )}
        </div>
      </Sheet>
    </>
  )
}
