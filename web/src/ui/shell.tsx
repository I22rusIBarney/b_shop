import { useEffect, useMemo } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { refreshBadges, useBadges } from '../lib/badges'
import { startParam } from '../lib/tg'
import { useApp } from '../lib/app'
import type { Branding } from '../lib/api'
import { Chat, Home, HomeFill, Shield, User } from './icons'

function Media({ src, className }: { src: string; className: string }) {
  if (!src) return null
  const isVideo = /\.(mp4|webm)$/i.test(src) || src.includes('video')
  return isVideo ? (
    <video className={className} src={src} autoPlay muted loop playsInline />
  ) : (
    <img className={className} src={src} alt="" />
  )
}

export function Backdrop({ b, dim }: { b: Branding; dim?: boolean }) {
  if (!b.bg_mobile && !b.bg_desktop) return <div className="backdrop" />
  return (
    <div className={dim ? 'backdrop dim' : 'backdrop'} style={{ opacity: b.bg_opacity / 100, ['--bg-fit' as any]: b.bg_fit, ['--bg-dim' as any]: `${b.bg_dim}%` }}>
      {b.bg_desktop && <Media src={b.bg_desktop} className="desktop" />}
      <Media src={b.bg_mobile || b.bg_desktop} className="mobile" />
    </div>
  )
}

const glyphs: Record<string, string[]> = {
  snow: ['❄', '❅', '❆', '•'],
  leaves: ['🍂', '🍁'],
  halloween: ['🦇', '🎃', '🕸'],
}

export function Effect({ kind }: { kind: string }) {
  const items = useMemo(() => {
    const g = glyphs[kind]
    if (!g) return []
    return Array.from({ length: kind === 'snow' ? 26 : 12 }, (_, i) => ({
      ch: g[i % g.length],
      left: Math.random() * 100,
      dur: 9 + Math.random() * 10,
      delay: -Math.random() * 18,
      size: 10 + Math.random() * 12,
    }))
  }, [kind])
  if (!items.length) return null
  return (
    <div className="fx" aria-hidden="true">
      {items.map((it, i) => (
        <i key={i} style={{ left: `${it.left}%`, animationDuration: `${it.dur}s`, animationDelay: `${it.delay}s`, fontSize: it.size }}>
          {it.ch}
        </i>
      ))}
    </div>
  )
}

/** Space at the top of home/login so the brand in the background art stays visible. */
export function HeroSpace() {
  const { config } = useApp()
  const b = config.branding
  if (b.show_header || !b.hero_space) return null
  return <div className="hero-space" style={{ height: b.hero_space }} aria-hidden="true" />
}

/** Logo + name. In the client shell it is shown only when enabled in branding. */
export function Brand({ always }: { always?: boolean }) {
  const { config } = useApp()
  const b = config.branding
  if (!always && !b.show_header) return null
  return (
    <div className="brand">
      <img src={b.logo || '/logo-default.png'} alt="" />
      <div>
        <div className="brand-name">{b.title || config.shop_name}</div>
        {b.tagline && <div className="brand-tag">{b.tagline}</div>}
      </div>
    </div>
  )
}

// Deep links from the bot: ?startapp=plans | referrals | support | news | ticket_<id>
const startRoutes: Record<string, string> = { plans: '/buy', buy: '/buy', referrals: '/referral', referral: '/referral', support: '/support', news: '/news', profile: '/profile' }
let startHandled = false

function useStartRoute() {
  const nav = useNavigate()
  useEffect(() => {
    if (startHandled) return
    startHandled = true
    const p = startParam()
    const m = /^ticket_(\d+)$/.exec(p)
    if (m) nav(`/support/${m[1]}`, { replace: true })
    else if (startRoutes[p]) nav(startRoutes[p], { replace: true })
  }, [nav])
}

export function ClientShell() {
  const { config, me } = useApp()
  const badges = useBadges()
  const loc = useLocation()
  useStartRoute()
  useEffect(() => {
    refreshBadges()
  }, [loc.pathname])
  return (
    <>
      <Backdrop b={config.branding} dim={loc.pathname !== '/'} />
      <Effect kind={config.branding.effect} />
      <main className="app">
        <Brand />
        <Outlet />
      </main>
      <nav className="tabbar" aria-label="Разделы">
        <NavLink to="/" end aria-label="Главная">
          {({ isActive }) => (isActive ? <HomeFill /> : <Home />)}
        </NavLink>
        <NavLink to="/support" aria-label="Поддержка">
          <Chat />
          {badges.tickets > 0 && <span className="nav-badge">{badges.tickets}</span>}
        </NavLink>
        <NavLink to="/profile" aria-label="Профиль">
          <User />
        </NavLink>
        {me?.customer.is_admin && (
          <NavLink to="/admin" aria-label="Админка">
            <Shield />
            {!!badges.admin_tickets && <span className="nav-badge">{badges.admin_tickets}</span>}
          </NavLink>
        )}
      </nav>
    </>
  )
}
