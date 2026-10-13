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

export function Toggle({ checked, onChange, label, hint }: { checked: boolean; onChange: (v: boolean) => void; label: ReactNode; hint?: ReactNode }) {
  return (
    <label className="toggle">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
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

export function Sheet({ open, onClose, title, children }: { open: boolean; onClose: () => void; title?: ReactNode; children: ReactNode }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])
  if (!open) return null
  return (
    <div className="sheet-wrap" onClick={onClose}>
      <div className="sheet" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <div className="spread" style={{ marginBottom: 14 }}>
          <h2>{title}</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Закрыть">
            <Close />
          </button>
        </div>
        {children}
      </div>
    </div>
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
  const reload = useCallback(async () => {
    setLoading(true)
    try {
      setData(await fn())
      setError('')
    } catch (e: any) {
      setError(e?.message || 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  useEffect(() => {
    reload()
  }, [reload])
  return { data, setData, error, loading, reload }
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
