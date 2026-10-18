import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { get, post, type Banner } from '../lib/api'
import { useApp } from '../lib/app'
import { openLink } from '../lib/tg'
import { LoginsSheet } from '../pages/Profile'
import { Chevron, Close, Gift, Help, Mail, News, Send, Star, Warn } from './icons'
import { useLoad } from './kit'

export const bannerIcons: Record<string, { label: string; icon: typeof Help }> = {
  info: { label: 'Инфо', icon: Help },
  gift: { label: 'Подарок', icon: Gift },
  star: { label: 'Звезда', icon: Star },
  warning: { label: 'Внимание', icon: Warn },
  mail: { label: 'Почта', icon: Mail },
  telegram: { label: 'Telegram', icon: Send },
  news: { label: 'Новость', icon: News },
}

export function BannerView({ b, onAction, onClose }: { b: Banner; onAction?: () => void; onClose?: () => void }) {
  const Icon = (bannerIcons[b.icon] || bannerIcons.info).icon
  const clickable = !!onAction && (!!b.button_url || b.kind !== '')
  return (
    <div className={`cab-banner ${b.icon}`}>
      <span className="cab-banner-ico" aria-hidden="true">
        <Icon />
      </span>
      <button type="button" className="cab-banner-body" onClick={onAction} disabled={!clickable}>
        <b>{b.title}</b>
        {b.subtitle && <span>{b.subtitle}</span>}
      </button>
      {b.button_text && clickable ? (
        <button type="button" className="cab-banner-btn" onClick={onAction}>
          {b.button_text}
        </button>
      ) : clickable ? (
        <Chevron className="cab-banner-chev" />
      ) : null}
      {b.dismissible && onClose && (
        <button type="button" className="cab-banner-x" onClick={onClose} aria-label="Скрыть">
          <Close />
        </button>
      )}
    </div>
  )
}

/** Banners above the subscription card (admin → Баннеры кабинета). */
export function CabinetBanners() {
  const { refreshMe } = useApp()
  const nav = useNavigate()
  const list = useLoad(() => get<Banner[]>('/api/banners'))
  const [hidden, setHidden] = useState<number[]>([])
  const [logins, setLogins] = useState(false)
  const items = (list.data || []).filter((b) => !hidden.includes(b.id))
  if (!items.length && !logins) return null

  const act = (b: Banner) => {
    if (b.kind === 'link_email' || b.kind === 'link_telegram') return setLogins(true)
    const u = b.button_url
    if (!u) return
    if (u.startsWith('/')) nav(u)
    else openLink(u)
  }
  const close = (b: Banner) => {
    setHidden((h) => [...h, b.id])
    post(`/api/banners/${b.id}/dismiss`).catch(() => {})
  }

  return (
    <div className="cab-banners">
      {items.map((b) => (
        <BannerView key={b.id} b={b} onAction={() => act(b)} onClose={() => close(b)} />
      ))}
      <LoginsSheet
        open={logins}
        onClose={() => setLogins(false)}
        onChanged={async () => {
          await refreshMe()
          await list.reload()
        }}
      />
    </div>
  )
}
