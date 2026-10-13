import type { Branding } from './api'

/** Pushes branding colors into CSS custom properties. */
export function applyBranding(b: Branding) {
  const r = document.documentElement.style
  const c = b.colors
  r.setProperty('--accent', c.primary)
  r.setProperty('--bg', c.background)
  r.setProperty('--surface-solid', c.surface)
  r.setProperty('--text', c.text)
  r.setProperty('--muted', c.muted)
  r.setProperty('--card-alpha', `${Math.max(0, Math.min(100, b.card_opacity))}%`)
  r.setProperty('--card-blur', `${b.card_blur}px`)
  r.setProperty('--accent-ink', readableInk(c.primary))
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', c.background)
  if (b.title) document.title = b.title
}

/** Picks dark or light text for content placed on the accent color. */
function readableInk(hex: string): string {
  const n = parseInt(hex.slice(1), 16)
  const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  const l = 0.2126 * r + 0.7152 * g + 0.0722 * b
  return l > 0.4 ? '#1b1005' : '#fffaf2'
}
