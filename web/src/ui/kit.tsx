import { createPortal } from 'react-dom'
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { Close } from './icons'

export function Spinner({ large }: { large?: boolean }) {
  return <span className={large ? 'spinner lg' : 'spinner'} role="status" aria-label="Загрузка" />
}

export function PageLoader() {
  return (
    <div className="page-center">
      <Spinner large />
    </div>
  )
}

export function Toggle({ checked, onChange, label, hint, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: ReactNode; hint?: ReactNode; disabled?: boolean }) {
  return (
    <label className={disabled ? 'toggle disabled' : 'toggle'}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} aria-label={typeof label === 'string' && label ? undefined : 'Переключатель'} />
      <span className="track" />
      <span className="grow">
        <span>{label}</span>
        {hint && <span className="hint tiny muted" style={{ display: 'block' }}>{hint}</span>}
      </span>
    </label>
  )
}

export function Field({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {hint && <span className="hint">{hint}</span>}
    </div>
  )
}

export function Sheet({ open, onClose, title, icon, children }: { open: boolean; onClose: () => void; title?: ReactNode; icon?: ReactNode; children: ReactNode }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])
  if (!open) return null
  // Portal: a sheet opened inside an animated (transformed) card must still cover the whole screen.
  return createPortal(
    <div className="sheet-wrap" onClick={onClose}>
      <div className="sheet" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <div className="sheet-head">
          <h2>
            {icon && <span className="sheet-ico">{icon}</span>}
            {title}
          </h2>
          <button className="icon-btn" onClick={onClose} aria-label="Закрыть">
            <Close />
          </button>
        </div>
        {children}
      </div>
    </div>,
    document.body,
  )
}

// ---- toasts ----

type ToastMsg = { text: string; error?: boolean; id: number }
const ToastCtx = createContext<(text: string, error?: boolean) => void>(() => {})

export function ToastProvider({ children }: { children: ReactNode }) {
  const [msg, setMsg] = useState<ToastMsg | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const show = useCallback((text: string, error?: boolean) => {
    window.clearTimeout(timer.current)
    setMsg({ text, error, id: Date.now() })
    timer.current = window.setTimeout(() => setMsg(null), error ? 5000 : 2500)
  }, [])
  return (
    <ToastCtx.Provider value={show}>
      {children}
      {msg && (
        <div key={msg.id} className={msg.error ? 'toast error' : 'toast'} role="status" onClick={() => setMsg(null)}>
          {msg.text}
        </div>
      )}
    </ToastCtx.Provider>
  )
}

export const useToast = () => useContext(ToastCtx)

/** Runs an async action with busy state and error toast. */
export function useAction() {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const run = useCallback(
    async <T,>(fn: () => Promise<T>, ok?: string): Promise<T | undefined> => {
      setBusy(true)
      try {
        const r = await fn()
        if (ok) toast(ok)
        return r
      } catch (e: any) {
        toast(e?.message || 'Что-то пошло не так', true)
        return undefined
      } finally {
        setBusy(false)
      }
    },
    [toast],
  )
  return { busy, run }
}

/** Loads data once (and on deps change) with loading/error state. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | undefined>()
  const [error, setError] = useState<string>('')
  const [loading, setLoading] = useState(true)
  // Bumped when deps change, so an answer for the previous page (old ticket id) is dropped.
  const gen = useRef(0)
  const reload = useCallback(async () => {
    const g = ++gen.current
    setLoading(true)
    try {
      const v = await fn()
      if (g !== gen.current) return
      setData(v)
      setError('')
    } catch (e: any) {
      if (g === gen.current) setError(e?.message || 'Ошибка загрузки')
    } finally {
      if (g === gen.current) setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  /** Background refresh: no loader, and a failed request keeps what is on screen. */
  const refresh = useCallback(async () => {
    const g = gen.current
    try {
      const v = await fn()
      if (g === gen.current) setData(v)
    } catch {
      /* keep the current data; the next tick tries again */
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  useEffect(() => {
    reload()
  }, [reload])
  return { data, setData, error, loading, reload, refresh }
}

/**
 * Calls `tick` every `ms` while the page is visible, and right away when the user comes back to it.
 * One timer per component, and a tick never starts while the previous one is still running.
 */
export function usePolling(tick: () => Promise<unknown>, ms: number) {
  const fn = useRef(tick)
  fn.current = tick
  useEffect(() => {
    let running = false
    let stopped = false
    const run = async () => {
      if (running || stopped || document.visibilityState !== 'visible') return
      running = true
      try {
        await fn.current()
      } catch {
        /* ignore: polling must never break the page */
      } finally {
        running = false
      }
    }
    const timer = window.setInterval(run, ms)
    const onShow = () => document.visibilityState === 'visible' && run()
    document.addEventListener('visibilitychange', onShow)
    window.addEventListener('online', onShow)
    return () => {
      stopped = true
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onShow)
      window.removeEventListener('online', onShow)
    }
  }, [ms])
}

export function ErrorBox({ text, retry }: { text: string; retry?: () => void }) {
  return (
    <div className="card empty">
      <h3>Не удалось загрузить</h3>
      <p className="small">{text}</p>
      {retry && (
        <button className="btn secondary small" style={{ marginTop: 12 }} onClick={retry}>
          Повторить
        </button>
      )}
    </div>
  )
}

export async function copy(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}
