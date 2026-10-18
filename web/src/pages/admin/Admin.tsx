import { type ComponentType, useEffect, useRef, useState } from 'react'
import { NavLink, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { useApp } from '../../lib/app'
import {
  Bars, Bell, Book, Card, Chat, ChevronDown, Coin, Download, Exit, Fingerprint, Globe, Shield, Headset, Hook, List, Megaphone, Menu, PanelLeft,
  Palette, People, Receipt, Renew, Settings as SettingsIcon, Sliders, Star, Swap, Ticket, Trend, Trial, Users, Wallet,
} from '../../ui/icons'
import Customers from './Customers'
import CustomerCard from './CustomerCard'
import Payments from './Payments'
import Plans from './Plans'
import PlanEditor from './PlanEditor'
import SettingsPage from './Settings'
import BrandingPage from './Branding'
import Promos from './Promos'
import { TicketChat, TicketList } from './Tickets'
import Knowledge from './Knowledge'
import Broadcasts from './Broadcasts'
import Automations from './Automations'
import Notifications from './Notifications'
import Stats from './Stats'
import Webhooks from './Webhooks'
import Banners from './Banners'
import ImportRWP from './ImportRWP'
import { useBadges } from '../../lib/badges'

type Item = { to: string; label: string; icon: ComponentType<{ className?: string }>; badge?: 'tickets' }
type Group = { title: string; items: Item[]; stats?: boolean }

// Same order and grouping as the reference menu: sections with a bar, dashed dividers, Statistics unfolds.
const statsNav: Item[] = [
  { to: '/admin/stats/overview', label: 'Обзор', icon: Bars },
  { to: '/admin/stats/economics', label: 'Юнит-экономика', icon: Coin },
  { to: '/admin/stats/payments', label: 'Платежи', icon: Card },
  { to: '/admin/stats/referrals', label: 'Рефералы', icon: People },
  { to: '/admin/stats/support', label: 'Поддержка', icon: Headset },
  { to: '/admin/stats/analytics', label: 'Аналитика', icon: Trend },
  { to: '/admin/stats/geo', label: 'География', icon: Globe },
]
const groups: Group[] = [
  { title: 'Обзор', stats: true, items: [{ to: '/admin/payments', label: 'Платежи', icon: Wallet }] },
  { title: 'Аудитория', items: [{ to: '/admin/customers', label: 'Клиенты', icon: Users }] },
  {
    title: 'Маркетинг',
    items: [
      { to: '/admin/broadcasts', label: 'Рассылки', icon: Megaphone },
      { to: '/admin/promos', label: 'Промокоды', icon: Ticket },
      { to: '/admin/automations', label: 'Автоматизации', icon: Renew },
      { to: '/admin/banners', label: 'Баннеры кабинета', icon: Bars },
      { to: '/admin/settings/automations', label: 'Триггеры и рассылки', icon: Sliders },
    ],
  },
  {
    title: 'Поддержка',
    items: [
      { to: '/admin/tickets', label: 'Обращения', icon: Chat, badge: 'tickets' },
      { to: '/admin/support/knowledge', label: 'Знания', icon: Book },
      { to: '/admin/settings/support', label: 'Поддержка и ИИ', icon: Sliders },
    ],
  },
  {
    title: 'Настройки',
    items: [
      { to: '/admin/settings/general', label: 'Общие', icon: SettingsIcon },
      { to: '/admin/plans', label: 'Тарифы', icon: Receipt },
      { to: '/admin/settings/payments', label: 'Оплата и ЮKassa', icon: Card },
      { to: '/admin/settings/moynalog', label: 'Мой налог', icon: List },
      { to: '/admin/branding', label: 'Оформление', icon: Palette },
      { to: '/admin/settings/trial', label: 'Пробный период', icon: Trial },
      { to: '/admin/settings/referral', label: 'Рефералка и подарки', icon: People },
      { to: '/admin/settings/links', label: 'Ссылки и установка', icon: List },
      { to: '/admin/notifications', label: 'Уведомления', icon: Bell },
      { to: '/admin/webhooks', label: 'Webhooks', icon: Hook },
      { to: '/admin/import', label: 'Перенос из RWP', icon: Download },
    ],
  },
  {
    title: 'Безопасность',
    items: [
      { to: '/admin/settings/auth', label: 'Вход и почта', icon: Fingerprint },
      { to: '/admin/settings/backups', label: 'Резервные копии', icon: Shield },
    ],
  },
]

function AdminMenu({ open }: { open: boolean }) {
  const { config } = useApp()
  const b = config.branding
  const badges = useBadges()
  const loc = useLocation()
  const inStats = loc.pathname.startsWith('/admin/stats')
  const [statsOpen, setStatsOpen] = useState(inStats)
  useEffect(() => {
    if (inStats) setStatsOpen(true)
  }, [inStats])
  const body = useRef<HTMLDivElement>(null)
  // Opening the drawer shows the current page, not the top of a long list.
  useEffect(() => {
    if (!open) return
    const a = body.current?.querySelector<HTMLElement>('a.active:not(.am-parent)')
    a?.scrollIntoView({ block: 'center' })
  }, [open])

  const link = (n: Item, sub?: boolean) => (
    <NavLink key={n.to} to={n.to} className={sub ? 'am-item am-sub' : 'am-item'}>
      <n.icon className="am-ico" />
      <span className="am-label">{n.label}</span>
      {n.badge === 'tickets' && !!badges.admin_tickets && <span className="am-badge">{badges.admin_tickets}</span>}
    </NavLink>
  )

  return (
    <nav className={open ? 'admin-nav open' : 'admin-nav'} aria-label="Админка">
      <div className="am-head">
        <img src={b.logo || '/logo-default.png'} alt="" />
        <span>{b.title || config.shop_name}</span>
      </div>
      <div className="am-body" ref={body}>
        {groups.map((g, i) => (
          <section key={g.title} className="am-group">
            {i > 0 && <hr className="am-sep" />}
            <div className="am-title">{g.title}</div>
            {g.stats && (
              <>
                <button
                  type="button"
                  className={inStats ? 'am-item am-parent active' : 'am-item am-parent'}
                  aria-expanded={statsOpen}
                  onClick={() => setStatsOpen((v) => !v)}
                >
                  <Star className="am-ico" />
                  <span className="am-label">Статистика</span>
                  <ChevronDown className={statsOpen ? 'am-chev up' : 'am-chev'} />
                </button>
                {statsOpen && <div className="am-subs">{statsNav.map((n) => link(n, true))}</div>}
              </>
            )}
            {g.items.map((n) => link(n))}
          </section>
        ))}
      </div>
      <NavLink to="/" end className="am-foot">
        <Swap />
        Личный кабинет
      </NavLink>
    </nav>
  )
}

function readCollapsed() {
  try {
    return localStorage.getItem('bshop.admin.nav') === 'hidden'
  } catch {
    return false
  }
}

export default function Admin() {
  const { config, signOut } = useApp()
  const nav = useNavigate()
  const [open, setOpen] = useState(false)
  // Desktop: the side menu can be hidden for wide tables (reference: panel button left of the title).
  const [collapsed, setCollapsed] = useState(readCollapsed)
  const toggleNav = () =>
    setCollapsed((v) => {
      try {
        localStorage.setItem('bshop.admin.nav', v ? '' : 'hidden')
      } catch {
        /* storage blocked — keep it for this visit only */
      }
      return !v
    })
  const loc = useLocation()
  useEffect(() => setOpen(false), [loc.pathname])
  useEffect(() => {
    document.title = `Админка · ${config.branding.title}`
  }, [config.branding.title])

  return (
    <div className={collapsed ? 'admin collapsed' : 'admin'}>
      <header className="admin-top">
        <button className="icon-btn" onClick={() => setOpen(true)} aria-label="Меню">
          <Menu />
        </button>
        <strong>Админка</strong>
      </header>
      <div className={open ? 'admin-scrim open' : 'admin-scrim'} onClick={() => setOpen(false)} aria-hidden="true" />
      <AdminMenu open={open} />
      <main className="admin-main">
        <div className="admin-bar">
          <button
            className="icon-btn"
            onClick={() => (matchMedia('(max-width: 899px)').matches ? setOpen(true) : toggleNav())}
            aria-label="Меню"
            aria-expanded={open}
          >
            <PanelLeft className="only-desk" />
            <Menu className="only-phone" />
          </button>
          <span className="grow" />
          <button
            className="icon-btn"
            aria-label="Выйти"
            title="Выйти"
            onClick={async () => {
              await signOut()
              nav('/login')
            }}
          >
            <Exit />
          </button>
        </div>
        <Routes>
          <Route index element={<Navigate to="stats" replace />} />
          <Route path="stats" element={<Navigate to="overview" replace />} />
          <Route path="stats/:tab" element={<Stats />} />
          <Route path="customers" element={<Customers />} />
          <Route path="customers/:id" element={<CustomerCard />} />
          <Route path="payments" element={<Payments />} />
          <Route path="plans" element={<Plans />} />
          <Route path="plans/:id" element={<PlanEditor />} />
          <Route path="promos" element={<Promos />} />
          <Route path="branding" element={<BrandingPage />} />
          <Route path="tickets" element={<TicketList />} />
          <Route path="tickets/:id" element={<TicketChat />} />
          <Route path="support/knowledge" element={<Knowledge />} />
          <Route path="broadcasts" element={<Broadcasts />} />
          <Route path="banners" element={<Banners />} />
          <Route path="automations" element={<Automations />} />
          <Route path="notifications" element={<Notifications />} />
          <Route path="webhooks" element={<Webhooks />} />
          <Route path="import" element={<ImportRWP />} />
          <Route path="settings/:section" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="customers" replace />} />
        </Routes>
      </main>
    </div>
  )
}
