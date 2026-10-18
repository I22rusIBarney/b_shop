import { useEffect, useMemo, useState, type ComponentType, type MouseEvent } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { refreshBadges, useBadges } from '../lib/badges'
import { inTelegram, openLink, startParam } from '../lib/tg'
import { useApp } from '../lib/app'
import type { Branding } from '../lib/api'
import { AutopaySheet, LoginsSheet, PaymentsSheet, PromoSheet } from '../pages/Profile'
import { Card, Chat, Exit, External, Fingerprint, Help, Home, HomeFill, News, Send, Shield, Swap, Tag, User, Users, Wallet } from './icons'

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
      <ClientNav />
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

type Ico = ComponentType<{ className?: string }>
type SheetKind = '' | 'promo' | 'payments' | 'logins' | 'cards'

/** Desktop side menu of the cabinet (reference: grouped sections with a bar, dashed dividers, admin link at the
 *  bottom). Phones and Telegram keep the bottom tab bar; this menu is hidden below 1024 px. */
function ClientNav() {
  const { config, me, signOut, refreshMe } = useApp()
  const badges = useBadges()
  const nav = useNavigate()
  const [sheet, setSheet] = useState<SheetKind>('')
  const b = config.branding
  if (!me) return null

  const link = (to: string, label: string, Icon: Ico, badge?: number) => (
    <NavLink key={to} to={to} end={to === '/'} className="am-item">
      <Icon className="am-ico" />
      <span className="am-label">{label}</span>
      {!!badge && <span className="am-badge">{badge}</span>}
    </NavLink>
  )
  const action = (label: string, Icon: Ico, onClick: () => void) => (
    <button key={label} type="button" className="am-item" onClick={onClick}>
      <Icon className="am-ico" />
      <span className="am-label">{label}</span>
    </button>
  )
  const external = (e: MouseEvent, url: string) => {
    if (!inTelegram()) return
    e.preventDefault()
    openLink(url)
  }
  const links = config.links.filter((l) => l.url)

  return (
    <nav className="admin-nav client-nav" aria-label="Меню кабинета">
      <div className="am-head">
        <img src={b.logo || '/logo-default.png'} alt="" />
        <span>{b.title || config.shop_name}</span>
      </div>
      <div className="am-body">
        <section className="am-group">
          <div className="am-title">Главная</div>
          {link('/', 'Личный кабинет', Home)}
          {link('/news', 'Новости', News)}
        </section>
        <section className="am-group">
          <hr className="am-sep" />
          <div className="am-title">Покупки и бонусы</div>
          {link('/buy', 'Купить подписку', Wallet)}
          {action('Применить промокод', Tag, () => setSheet('promo'))}
        </section>
        {me.referral_enabled && (
          <section className="am-group">
            <hr className="am-sep" />
            <div className="am-title">Программы</div>
            {link('/referral', 'Реферальная система', Users)}
          </section>
        )}
        <section className="am-group">
          <hr className="am-sep" />
          <div className="am-title">Помощь</div>
          {link('/support', 'Поддержка', Chat, badges.tickets)}
          {link('/faq', 'FAQ', Help)}
        </section>
        {links.length > 0 && (
          <section className="am-group">
            <hr className="am-sep" />
            <div className="am-title">Ссылки</div>
            {links.map((l, i) => (
              <a key={i} className="am-item" href={l.url} target="_blank" rel="noopener noreferrer" onClick={(e) => external(e, l.url)}>
                <Send className="am-ico" />
                <span className="am-label">{l.title}</span>
                <External className="am-ico am-ext" />
              </a>
            ))}
          </section>
        )}
        <section className="am-group">
          <hr className="am-sep" />
          <div className="am-title">Аккаунт</div>
          {link('/profile', 'Профиль', User)}
          {action('Безопасность', Fingerprint, () => setSheet('logins'))}
          {action('Платежи', Card, () => setSheet('payments'))}
          {action('Автопродление', Wallet, () => setSheet('cards'))}
          {action('Выйти', Exit, async () => {
            await signOut()
            nav('/login')
          })}
        </section>
      </div>
      {me.customer.is_admin && (
        <NavLink to="/admin" className="am-foot">
          <Swap />
          Админ панель
        </NavLink>
      )}
      <PromoSheet open={sheet === 'promo'} onClose={() => setSheet('')} />
      <PaymentsSheet open={sheet === 'payments'} onClose={() => setSheet('')} />
      <LoginsSheet open={sheet === 'logins'} onClose={() => setSheet('')} onChanged={refreshMe} />
      <AutopaySheet open={sheet === 'cards'} onClose={() => setSheet('')} />
    </nav>
  )
}
