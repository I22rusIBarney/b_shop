import { useState, type ComponentType } from 'react'
import { useNavigate } from 'react-router-dom'
import { useApp } from '../lib/app'
import { useBadges } from '../lib/badges'
import { inTelegram, openLink } from '../lib/tg'
import { BuyForSheet } from '../pages/Gifts'
import { AutopaySheet, LoginsSheet, PaymentsSheet, PromoSheet } from '../pages/Profile'
import { Card, Chat, Exit, Fingerprint, Gift, Help, Home, News, Send, Tag, User, Users, Wallet } from './icons'

export type MenuItem = {
  key: string
  label: string
  /** Shorter label for the desktop side menu. */
  short?: string
  icon: ComponentType<{ className?: string }>
  to?: string
  href?: string
  onClick?: () => void
  badge?: number
  /** Shown only in one of the two menus. */
  only?: 'desktop' | 'mobile'
}
export type MenuGroup = { title: string; items: MenuItem[] }

type SheetKind = '' | 'promo' | 'payments' | 'logins' | 'cards' | 'buyfor'

/** Sections of the cabinet, shared by the desktop side menu and the phone "Профиль" tab (same order as the
 *  reference). Only features that work are listed; dialogs live here so both menus open the same ones. */
export function useClientMenu() {
  const { config, me, signOut, refreshMe } = useApp()
  const badges = useBadges()
  const nav = useNavigate()
  const [sheet, setSheet] = useState<SheetKind>('')
  const open = (k: SheetKind) => () => setSheet(k)

  const groups: MenuGroup[] = []
  if (me) {
    groups.push({
      title: 'Главная',
      items: [
        { key: 'home', label: 'Личный кабинет', icon: Home, to: '/', only: 'desktop' },
        { key: 'news', label: 'Новости', icon: News, to: '/news' },
      ],
    })
    groups.push({
      title: 'Покупки и бонусы',
      items: [
        { key: 'buy', label: 'Купить подписку', icon: Wallet, to: '/buy', only: 'desktop' },
        { key: 'promo', label: 'Применить промокод', icon: Tag, onClick: open('promo') },
        ...(me.buy_for_other ? [{ key: 'buyfor', label: 'Купить другому', icon: User, onClick: open('buyfor') }] : []),
        ...(me.gifts_enabled ? [{ key: 'gifts', label: 'Мои подарки', icon: Gift, to: '/gifts' }] : []),
      ],
    })
    if (me.referral_enabled) groups.push({ title: 'Программы', items: [{ key: 'ref', label: 'Реферальная система', icon: Users, to: '/referral' }] })
    groups.push({
      title: 'Помощь',
      items: [
        { key: 'support', label: 'Поддержка', icon: Chat, to: '/support', badge: badges.tickets, only: 'desktop' },
        { key: 'faq', label: 'Часто задаваемые вопросы', short: 'FAQ', icon: Help, to: '/faq' },
      ],
    })
    const links = config.links.filter((l) => l.url)
    if (links.length) groups.push({ title: 'Ссылки', items: links.map((l, i) => ({ key: `link${i}`, label: l.title, icon: Send, href: l.url })) })
    groups.push({
      title: 'Аккаунт',
      items: [
        { key: 'logins', label: 'Вход в систему', icon: Fingerprint, onClick: open('logins') },
        { key: 'payments', label: 'Платежи', icon: Card, onClick: open('payments') },
        { key: 'cards', label: 'Автопродление', icon: Wallet, onClick: open('cards') },
        {
          key: 'exit',
          label: 'Выйти',
          icon: Exit,
          onClick: async () => {
            await signOut()
            nav('/login')
          },
        },
      ],
    })
  }

  const sheets = (
    <>
      <PromoSheet open={sheet === 'promo'} onClose={() => setSheet('')} />
      <PaymentsSheet open={sheet === 'payments'} onClose={() => setSheet('')} />
      <LoginsSheet open={sheet === 'logins'} onClose={() => setSheet('')} onChanged={refreshMe} />
      <AutopaySheet open={sheet === 'cards'} onClose={() => setSheet('')} />
      <BuyForSheet open={sheet === 'buyfor'} onClose={() => setSheet('')} />
    </>
  )
  return { groups, sheets }
}

/** External links open through Telegram inside the mini app, as a normal new tab elsewhere. */
export function openExternal(e: { preventDefault: () => void }, url: string) {
  if (!inTelegram()) return
  e.preventDefault()
  openLink(url)
}
