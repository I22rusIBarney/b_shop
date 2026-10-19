import { useEffect, useLayoutEffect, useRef, useState, type ComponentType, type ReactNode } from 'react'
import { Check, ChevronDown } from './icons'

type Ico = ComponentType<{ className?: string }>

/** Accent of a section's icon tile. The palette stays inside the reference's warm/dark world. */
export type Tone = 'amber' | 'green' | 'blue' | 'violet' | 'rose' | 'teal' | 'slate'

/** Admin panel section: icon tile, title, optional count and actions, a hairline, then content. */
export function Panel({
  icon: Icon,
  tone = 'amber',
  title,
  count,
  actions,
  wide,
  className,
  children,
}: {
  icon: Ico
  tone?: Tone
  title: ReactNode
  count?: number
  actions?: ReactNode
  wide?: boolean
  className?: string
  children: ReactNode
}) {
  return (
    <section className={['panel', wide && 'wide', className].filter(Boolean).join(' ')}>
      <header className="panel-head">
        <span className={`tile ${tone}`} aria-hidden="true">
          <Icon />
        </span>
        <h3>{title}</h3>
        {!!count && <span className="panel-count num">{count}</span>}
        {actions && <div className="panel-act">{actions}</div>}
      </header>
      <div className="panel-body">{children}</div>
    </section>
  )
}

/** Page switcher for long admin lists; hidden when everything fits on one page. */
export function Pager({ offset, count, total, size, onOffset }: { offset: number; count: number; total: number; size: number; onOffset: (o: number) => void }) {
  if (total <= size && offset === 0) return null
  return (
    <div className="pager">
      <span className="small muted num">
        {count ? `${offset + 1}–${offset + count}` : 0} из {total.toLocaleString('ru-RU')}
      </span>
      <button className="btn secondary small" disabled={offset === 0} onClick={() => onOffset(Math.max(0, offset - size))}>
        Назад
      </button>
      <button className="btn secondary small" disabled={offset + size >= total} onClick={() => onOffset(offset + size)}>
        Дальше
      </button>
    </div>
  )
}

/** Empty state inside a panel: a quiet icon in a circle and one line. */
export function Empty({ icon: Icon, children }: { icon: Ico; children: ReactNode }) {
  return (
    <div className="panel-empty">
      <span aria-hidden="true">
        <Icon />
      </span>
      <p>{children}</p>
    </div>
  )
}

/** Label/value row with a small leading icon (profile facts, ticket sidebar). */
export function InfoRow({ icon: Icon, label, children }: { icon?: Ico; label: ReactNode; children: ReactNode }) {
  return (
    <div className="info-row">
      <span className="info-label">
        {Icon && <Icon />}
        {label}
      </span>
      <span className="info-value">{children}</span>
    </div>
  )
}

const avatarHues = [28, 200, 150, 280, 340, 45, 175]

/** Initials avatar; the hue is stable per person so the same customer is recognisable across lists. */
export function Avatar({ name, seed, size = 36, photo = true }: { name: string; seed: number | string; size?: number; photo?: boolean }) {
  const parts = name.replace(/^@/, '').trim().split(/\s+/).filter(Boolean)
  const initials = ((parts[0]?.[0] || '?') + (parts[1]?.[0] || '')).toUpperCase()
  const n = typeof seed === 'number' ? seed : [...String(seed)].reduce((a, ch) => a + ch.charCodeAt(0), 0)
  const hue = avatarHues[Math.abs(n) % avatarHues.length]
  // Numeric seed = customer id: try the Telegram profile photo, keep the initials underneath until it loads.
  const [state, setState] = useState<'load' | 'ok' | 'none'>(photo && typeof seed === 'number' && seed > 0 ? (noPhoto.has(seed) ? 'none' : 'load') : 'none')
  return (
    <span className={state === 'ok' ? 'avatar has-photo' : 'avatar'} style={{ width: size, height: size, fontSize: size * 0.38, ['--h' as string]: hue }} aria-hidden="true">
      {initials}
      {state !== 'none' && (
        <img
          src={`/api/avatars/${seed}`}
          alt=""
          loading="lazy"
          decoding="async"
          onLoad={() => setState('ok')}
          onError={() => {
            noPhoto.add(seed as number)
            setState('none')
          }}
        />
      )}
    </span>
  )
}

// Customers without a photo, remembered for the session so lists do not ask again on every render.
const noPhoto = new Set<number>()

export type SwitchOption = { value: string; label: string; short?: string; count?: number }

/**
 * One control for "pick one of N": a segmented control with a sliding thumb while it fits,
 * and a compact menu button ("Current ▾") when it does not — so a row of choices never spills off screen.
 */
export function Switcher({
  options,
  value,
  onChange,
  label,
  menu,
  className,
}: {
  options: SwitchOption[]
  value: string
  onChange: (v: string) => void
  label: string
  /** always render as a menu button (secondary filters) */
  menu?: boolean
  className?: string
}) {
  const wrap = useRef<HTMLDivElement>(null)
  const track = useRef<HTMLDivElement>(null)
  const [compact, setCompact] = useState(!!menu)
  const [thumb, setThumb] = useState<{ x: number; w: number } | null>(null)
  const [open, setOpen] = useState(false)
  const current = options.find((o) => o.value === value) || options[0]

  // Measure the full segmented control against the space it has; fall back to the menu when it would overflow.
  useLayoutEffect(() => {
    if (menu) return
    const el = wrap.current
    const t = track.current
    if (!el || !t) return
    // In a flex toolbar the menu button is narrower than the control it replaces, so judge the room by the toolbar
    // there; in a grid cell or a block the switcher's own width does not depend on its state.
    const host = el.parentElement
    const flexHost = !!host && getComputedStyle(host).display.includes('flex')
    const check = () => setCompact((c) => t.scrollWidth > (c && flexHost && host ? host.clientWidth : el.clientWidth) + 1)
    const ro = new ResizeObserver(check)
    ro.observe(el)
    if (flexHost && host) ro.observe(host)
    check()
    return () => ro.disconnect()
  }, [menu, options.length])

  useLayoutEffect(() => {
    const t = track.current
    if (compact || !t) return
    const btn = t.querySelector<HTMLElement>('[aria-checked="true"]')
    if (btn) setThumb({ x: btn.offsetLeft, w: btn.offsetWidth })
  }, [value, compact, options])

  useEffect(() => {
    if (!open) return
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent && e.key !== 'Escape') return
      if (e instanceof MouseEvent && wrap.current?.contains(e.target as Node)) return
      setOpen(false)
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', close)
    }
  }, [open])

  const count = (o: SwitchOption) => (o.count ? <span className="sw-count num">{o.count}</span> : null)

  return (
    <div className={['switcher', compact && 'is-menu', className].filter(Boolean).join(' ')} ref={wrap}>
      <div className="sw-track" ref={track} role="radiogroup" aria-label={label} aria-hidden={compact || undefined}>
        {thumb && !compact && <span className="sw-thumb" style={{ transform: `translateX(${thumb.x}px)`, width: thumb.w }} />}
        {options.map((o) => (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={o.value === value}
            tabIndex={compact ? -1 : undefined}
            className="sw-opt"
            onClick={() => onChange(o.value)}
          >
            {o.short || o.label}
            {count(o)}
          </button>
        ))}
      </div>
      {compact && (
        <>
          <button type="button" className="sw-menu-btn" aria-haspopup="listbox" aria-expanded={open} aria-label={label} onClick={() => setOpen((v) => !v)}>
            <span>{current?.label}</span>
            {current && count(current)}
            <ChevronDown />
          </button>
          {open && (
            <div className="sw-pop" role="listbox" aria-label={label}>
              {options.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  role="option"
                  aria-selected={o.value === value}
                  className="sw-pop-item"
                  onClick={() => {
                    onChange(o.value)
                    setOpen(false)
                  }}
                >
                  <span className="grow">{o.label}</span>
                  {count(o)}
                  {o.value === value && <Check />}
                </button>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  )
}
