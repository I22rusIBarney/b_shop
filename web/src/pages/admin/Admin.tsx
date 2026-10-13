import { useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useApp } from '../../lib/app'
import { Brand } from '../../ui/shell'
import { Card, Home, List, Menu, Palette, Settings as SettingsIcon, Tag, Users, Wallet } from '../../ui/icons'
import Customers from './Customers'
import CustomerCard from './CustomerCard'
import Payments from './Payments'
import Plans from './Plans'
import PlanEditor from './PlanEditor'
import SettingsPage from './Settings'
import BrandingPage from './Branding'

const nav = [
  { to: '/admin/customers', label: 'Клиенты', icon: Users },
  { to: '/admin/payments', label: 'Платежи', icon: Wallet },
  { to: '/admin/plans', label: 'Тарифы', icon: Tag },
]
const settingsNav = [
  { to: '/admin/settings/general', label: 'Общие', icon: SettingsIcon },
  { to: '/admin/branding', label: 'Оформление', icon: Palette },
  { to: '/admin/settings/payments', label: 'Оплата и ЮKassa', icon: Card },
  { to: '/admin/settings/moynalog', label: 'Мой налог', icon: List },
  { to: '/admin/settings/auth', label: 'Вход и почта', icon: Users },
  { to: '/admin/settings/trial', label: 'Пробный период', icon: Tag },
  { to: '/admin/settings/referral', label: 'Рефералка', icon: Users },
  { to: '/admin/settings/links', label: 'Ссылки и установка', icon: List },
]

export default function Admin() {
  const { config } = useApp()
  const [open, setOpen] = useState(false)
  const loc = useLocation()
  useEffect(() => setOpen(false), [loc.pathname])
  useEffect(() => {
    document.title = `Админка · ${config.branding.title}`
  }, [config.branding.title])

  return (
    <div className="admin">
      <header className="admin-top">
        <button className="icon-btn" onClick={() => setOpen(true)} aria-label="Меню">
          <Menu />
        </button>
        <strong>Админка</strong>
      </header>
      <nav className={open ? 'admin-nav open' : 'admin-nav'} aria-label="Админка" onClick={(e) => e.target === e.currentTarget && setOpen(false)}>
        <Brand />
        {nav.map((n) => (
          <NavLink key={n.to} to={n.to}>
            <n.icon />
            {n.label}
          </NavLink>
        ))}
        <div className="group">Настройки</div>
        {settingsNav.map((n) => (
          <NavLink key={n.to} to={n.to}>
            <n.icon />
            {n.label}
          </NavLink>
        ))}
        <div className="group" />
        <NavLink to="/" end>
          <Home />
          Личный кабинет
        </NavLink>
      </nav>
      <main className="admin-main">
        <Routes>
          <Route index element={<Navigate to="customers" replace />} />
          <Route path="customers" element={<Customers />} />
          <Route path="customers/:id" element={<CustomerCard />} />
          <Route path="payments" element={<Payments />} />
          <Route path="plans" element={<Plans />} />
          <Route path="plans/:id" element={<PlanEditor />} />
          <Route path="branding" element={<BrandingPage />} />
          <Route path="settings/:section" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="customers" replace />} />
        </Routes>
      </main>
    </div>
  )
}
