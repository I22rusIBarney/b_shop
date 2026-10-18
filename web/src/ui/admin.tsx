import type { ComponentType, ReactNode } from 'react'

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
        {count != null && <span className="panel-count num">{count}</span>}
        {actions && <div className="panel-act">{actions}</div>}
      </header>
      <div className="panel-body">{children}</div>
    </section>
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
export function Avatar({ name, seed, size = 36 }: { name: string; seed: number | string; size?: number }) {
  const parts = name.replace(/^@/, '').trim().split(/\s+/).filter(Boolean)
  const initials = ((parts[0]?.[0] || '?') + (parts[1]?.[0] || '')).toUpperCase()
  const n = typeof seed === 'number' ? seed : [...String(seed)].reduce((a, ch) => a + ch.charCodeAt(0), 0)
  const hue = avatarHues[Math.abs(n) % avatarHues.length]
  return (
    <span className="avatar" style={{ width: size, height: size, fontSize: size * 0.38, ['--h' as string]: hue }} aria-hidden="true">
      {initials}
    </span>
  )
}
