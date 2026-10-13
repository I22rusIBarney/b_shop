import { useMemo } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { useApp } from '../lib/app'
import type { Branding } from '../lib/api'
import { Chat, Home, Shield, User } from './icons'

function Media({ src, className }: { src: string; className: string }) {
  if (!src) return null
  const isVideo = /\.(mp4|webm)$/i.test(src) || src.includes('video')
  return isVideo ? (
    <video className={className} src={src} autoPlay muted loop playsInline />
  ) : (
    <img className={className} src={src} alt="" />
  )
}

export function Backdrop({ b }: { b: Branding }) {
  if (!b.bg_mobile && !b.bg_desktop) return <div className="backdrop" />
  return (
    <div className="backdrop" style={{ opacity: b.bg_opacity / 100, ['--bg-fit' as any]: b.bg_fit }}>
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

export function Brand() {
  const { config } = useApp()
  const b = config.branding
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

export function ClientShell() {
  const { config, me } = useApp()
  return (
    <>
      <Backdrop b={config.branding} />
      <Effect kind={config.branding.effect} />
      <main className="app">
        <Brand />
        <Outlet />
      </main>
      <nav className="tabbar" aria-label="Разделы">
        <NavLink to="/" end>
          <Home />
          Главная
        </NavLink>
        <NavLink to="/support">
          <Chat />
          Поддержка
        </NavLink>
        <NavLink to="/profile">
          <User />
          Профиль
        </NavLink>
        {me?.customer.is_admin && (
          <NavLink to="/admin">
            <Shield />
            Админка
          </NavLink>
        )}
      </nav>
    </>
  )
}
