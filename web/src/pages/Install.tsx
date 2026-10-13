import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import QRCode from 'qrcode'
import DOMPurify from 'dompurify'
import { createHappCryptoLink } from '@kastov/cryptohapp'
import { get, type Subscription } from '../lib/api'
import { useApp } from '../lib/app'
import { haptic, inTelegram, openAppLink, openLink } from '../lib/tg'
import { Back, Check, Chevron, Copy, Download, Link as LinkIcon, Phone, Plus } from '../ui/icons'
import { ErrorBox, PageLoader, Sheet, copy, useLoad, useToast } from '../ui/kit'

// The install page works like the subscription page of Remnawave: it detects the device, offers the guide
// for it ("Продолжить" → steps) and lets the customer pick another device or share the link by QR.
// Steps come from the subscription page config of the customer's squad, or from the shop's own app list.

// ---- Remnawave subscription page config (v1) ----

type L10n = Record<string, string>
interface RwButton {
  link: string
  type: 'external' | 'subscriptionLink' | 'copyButton'
  text: L10n
  svgIconKey: string
}
interface RwBlock {
  svgIconKey: string
  svgIconColor: string
  title: L10n
  description: L10n
  buttons: RwButton[]
}
interface RwApp {
  name: string
  svgIconKey?: string
  featured: boolean
  blocks: RwBlock[]
}
interface RwPlatform {
  displayName: L10n
  svgIconKey: string
  apps: RwApp[]
}
interface RwConfig {
  locales?: string[]
  svgLibrary?: Record<string, string>
  platforms: Record<string, RwPlatform>
}
interface Guide {
  source: 'remnawave' | 'manual'
  config?: RwConfig
  error?: string
}

// ---- normalized guide ----

interface GButton {
  label: string
  kind: 'add' | 'open' | 'copy'
  link: string
  icon?: string
}
interface GStep {
  title: string
  text: string
  icon?: string
  color?: string
  fallback: 'download' | 'add' | 'check'
  buttons: GButton[]
}
interface GApp {
  name: string
  featured: boolean
  steps: GStep[]
}
interface GPlatform {
  key: string
  name: string
  icon?: string
  apps: GApp[]
}

const platformOrder = ['ios', 'android', 'windows', 'macos', 'linux', 'androidTV', 'appleTV']
const manualPlatforms: [string, string][] = [
  ['ios', 'iPhone и iPad'],
  ['android', 'Android'],
  ['windows', 'Windows'],
  ['macos', 'macOS'],
  ['linux', 'Linux'],
  ['tv', 'Телевизор'],
]
const stepColors = ['#8b5cf6', '#14b8a6', '#22c55e', '#f59e0b', '#3b82f6']

function detect(): string {
  const tp = (window as any).Telegram?.WebApp?.platform as string | undefined
  if (tp === 'ios') return 'ios'
  if (tp === 'android' || tp === 'android_x') return 'android'
  if (tp === 'macos') return 'macos'
  const ua = navigator.userAgent
  if (/iPhone|iPad|iPod/i.test(ua)) return 'ios'
  if (/Macintosh/i.test(ua) && navigator.maxTouchPoints > 1) return 'ios' // iPad in desktop mode
  if (/Android/i.test(ua)) return /\bTV\b|AFT|BRAVIA|SmartTV/i.test(ua) ? 'androidTV' : 'android'
  if (/Mac OS X/i.test(ua)) return 'macos'
  if (/Windows/i.test(ua)) return 'windows'
  if (/Linux/i.test(ua)) return 'linux'
  return 'android'
}

function tr(t: L10n | undefined): string {
  if (!t) return ''
  return t.ru || t.en || Object.values(t)[0] || ''
}

function fill(template: string, url: string, username: string): string {
  return template.replace(/\{\{(\w+)\}\}/g, (m, key: string) => {
    switch (key) {
      case 'SUBSCRIPTION_LINK':
        return url
      case 'USERNAME':
        return username
      case 'HAPP_CRYPT3_LINK':
        return createHappCryptoLink(url, 'v3', true) || url
      case 'HAPP_CRYPT4_LINK':
        return createHappCryptoLink(url, 'v4', true) || url
      default:
        return m
    }
  })
}

function fromRemnawave(rw: RwConfig, url: string, username: string): GPlatform[] {
  const keys = platformOrder
    .filter((k) => rw.platforms[k]?.apps?.length)
    .concat(Object.keys(rw.platforms).filter((k) => !platformOrder.includes(k) && rw.platforms[k]?.apps?.length))
  return keys.map((k) => {
    const p = rw.platforms[k]
    return {
      key: k,
      name: tr(p.displayName) || k,
      icon: p.svgIconKey,
      apps: [...p.apps]
        .sort((a, b) => Number(b.featured) - Number(a.featured))
        .map((a) => ({
          name: a.name,
          featured: a.featured,
          steps: a.blocks.map((b, i) => ({
            title: tr(b.title),
            text: fill(tr(b.description), url, username),
            icon: b.svgIconKey,
            color: b.svgIconColor,
            fallback: i === 0 ? 'download' : i === a.blocks.length - 1 ? 'check' : 'add',
            buttons: b.buttons.map((btn) => ({
              label: tr(btn.text),
              kind: btn.type === 'copyButton' ? 'copy' : btn.type === 'subscriptionLink' ? 'add' : 'open',
              link: fill(btn.link, url, username),
              icon: btn.svgIconKey,
            })),
          })),
        })),
    }
  })
}

function fromManual(apps: { platform: string; name: string; store_url: string; deep_link: string }[], url: string, username: string): GPlatform[] {
  const deep = (t: string) => fill(t.replace('{url}', encodeURIComponent(url)).replace('{url_b64}', btoa(url)), url, username)
  return manualPlatforms.map(([key, name]) => {
    const list = apps.filter((a) => a.platform === key)
    const connect: GStep = { title: 'Подключитесь', text: 'Выберите сервер в приложении и нажмите кнопку подключения.', fallback: 'check', buttons: [] }
    const guides: GApp[] = list.length
      ? list.map((a, i) => ({
          name: a.name,
          featured: i === 0,
          steps: [
            { title: 'Установите приложение', text: `Скачайте ${a.name} и откройте его.`, fallback: 'download', buttons: a.store_url ? [{ label: 'Скачать', kind: 'open', link: a.store_url }] : [] },
            a.deep_link
              ? { title: 'Добавьте подписку', text: 'Нажмите кнопку ниже — подписка добавится в приложение сама.', fallback: 'add', buttons: [{ label: 'Добавить подписку', kind: 'add', link: deep(a.deep_link) }] }
              : { title: 'Добавьте подписку', text: 'Скопируйте ссылку и вставьте её в приложении: «Добавить» → «Из буфера обмена».', fallback: 'add', buttons: [{ label: 'Скопировать ссылку', kind: 'copy', link: url }] },
            connect,
          ],
        }))
      : [
          {
            name: 'Любой клиент',
            featured: true,
            steps: [
              { title: 'Установите приложение', text: 'Подойдёт любой клиент с поддержкой подписок Xray (VLESS).', fallback: 'download', buttons: [] },
              { title: 'Добавьте подписку', text: 'Скопируйте ссылку и вставьте её в приложении: «Добавить» → «Из буфера обмена».', fallback: 'add', buttons: [{ label: 'Скопировать ссылку', kind: 'copy', link: url }] },
              connect,
            ],
          },
        ]
    return { key, name, apps: guides }
  })
}

function Svg({ lib, k, color, size = 20 }: { lib?: Record<string, string>; k?: string; color?: string; size?: number }) {
  const html = useMemo(() => {
    const raw = k && lib?.[k]
    return raw ? DOMPurify.sanitize(raw, { USE_PROFILES: { svg: true, svgFilters: true } }) : ''
  }, [lib, k])
  if (!html) return null
  return <span aria-hidden="true" style={{ display: 'inline-flex', width: size, height: size, color: color || 'currentColor', flex: 'none' }} className="svg-slot" dangerouslySetInnerHTML={{ __html: html }} />
}

// Generic device pictures for platforms without an icon in the config.
const pathFor = (k: string): string[] => {
  if (/tv/i.test(k)) return ['M3 6h18v11H3z', 'M8 21h8', 'M12 17v4']
  if (k === 'windows' || k === 'linux') return ['M3 4h18v12H3z', 'M8 20h8', 'M12 16v4']
  if (k === 'macos') return ['M5 5h14v10H5z', 'M2 19h20']
  return ['M7 2h10v20H7z', 'M11 18h2']
}
function DeviceIcon({ k, size = 26 }: { k: string; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {pathFor(k).map((d) => (
        <path key={d} d={d} />
      ))}
    </svg>
  )
}

// Remnawave configs name Mantine colors ("violet", "teal"…) or give a hex.
const named: Record<string, string> = {
  violet: '#8b5cf6', grape: '#c026d3', indigo: '#6366f1', blue: '#3b82f6', cyan: '#06b6d4', teal: '#14b8a6', green: '#22c55e',
  lime: '#84cc16', yellow: '#eab308', orange: '#f97316', red: '#ef4444', pink: '#ec4899', gray: '#9ca3af', dark: '#9ca3af',
}
const hexColor = (c?: string) => (c && /^#[0-9a-f]{3,8}$/i.test(c) ? c : (c && named[c.toLowerCase()]) || '')

export default function Install() {
  const { id } = useParams()
  const nav = useNavigate()
  const toast = useToast()
  const { config } = useApp()
  const subs = useLoad(() => get<Subscription[]>('/api/subscriptions'))
  const guide = useLoad(() => get<Guide>(`/api/install-guide?subscription_id=${id || ''}`), [id])
  const [detected] = useState(detect)
  const [platform, setPlatform] = useState(detected)
  const [view, setView] = useState<'overview' | 'steps'>('overview')
  const [appIdx, setAppIdx] = useState(0)
  const [pickOpen, setPickOpen] = useState(false)
  const [qr, setQr] = useState('')
  const sub = subs.data?.find((s) => s.id === Number(id))
  const url = sub?.subscription_url || ''
  const b = config.branding

  useEffect(() => {
    // A slow render for the previous subscription must not overwrite the QR of the current one.
    let active = true
    setQr('')
    if (url)
      QRCode.toDataURL(url, { margin: 1, width: 480, errorCorrectionLevel: b.logo ? 'H' : 'M', color: { dark: '#141414', light: '#ffffff' } }).then(
        (v) => active && setQr(v),
        () => active && setQr(''),
      )
    return () => {
      active = false
    }
  }, [url, b.logo])
  useEffect(() => setAppIdx(0), [platform])
  useEffect(() => window.scrollTo(0, 0), [view])

  const rw = guide.data?.source === 'remnawave' ? guide.data.config : undefined
  const platforms = useMemo(
    () => (!url ? [] : rw ? fromRemnawave(rw, url, sub?.rw_username || '') : fromManual(config.install.apps, url, sub?.rw_username || '')),
    [rw, url, sub?.rw_username, config.install.apps],
  )

  if ((subs.loading && !subs.data) || (guide.loading && !guide.data)) return <PageLoader />
  if (subs.error) return <ErrorBox text={subs.error} retry={subs.reload} />
  if (!sub) return <ErrorBox text="Подписка не найдена" />

  const norm = (k: string) => (rw ? k : k === 'androidTV' || k === 'appleTV' ? 'tv' : k)
  const current = platforms.find((p) => p.key === norm(platform)) || platforms[0]
  const app = current?.apps[Math.min(appIdx, current.apps.length - 1)]
  const isDetected = current?.key === norm(detected)

  const copyLink = async (text = url) => {
    if (await copy(text)) {
      haptic('success')
      toast('Ссылка скопирована')
    }
  }
  const share = async () => {
    if (navigator.share) {
      try {
        await navigator.share({ url, title: b.title })
        return
      } catch (e) {
        if ((e as Error)?.name === 'AbortError') return
      }
    }
    if (inTelegram()) {
      openLink(`https://t.me/share/url?url=${encodeURIComponent(url)}`)
      return
    }
    copyLink()
  }
  const press = (btn: GButton) => {
    haptic('light')
    if (btn.kind === 'copy') return copyLink(btn.link)
    if (/^https?:/i.test(btn.link)) return openLink(btn.link)
    openAppLink(btn.link)
    if (!inTelegram()) {
      // In a browser an unknown scheme silently does nothing: if the page is still in front, explain.
      setTimeout(() => {
        if (document.visibilityState === 'visible') toast('Приложение не открылось? Сначала установите его, затем нажмите ещё раз')
      }, 1800)
    }
  }
  const platIcon = (p: GPlatform, size = 26) => (rw && p.icon && rw.svgLibrary?.[p.icon] ? <Svg lib={rw.svgLibrary} k={p.icon} size={size} /> : <DeviceIcon k={p.key} size={size} />)
  const stepIcon = (s: GStep) => {
    if (rw && s.icon && rw.svgLibrary?.[s.icon]) return <Svg lib={rw.svgLibrary} k={s.icon} size={22} />
    return s.fallback === 'download' ? <Download /> : s.fallback === 'add' ? <Plus /> : <Check />
  }

  const header = (title: string, back: () => void) => (
    <div className="row">
      <button className="icon-btn" onClick={back} aria-label="Назад">
        <Back />
      </button>
      <h1>{title}</h1>
    </div>
  )

  if (!current)
    return (
      <div className="stack lg">
        {header('Установка и настройка', () => nav(-1))}
        <div className="card empty">Инструкций пока нет. Скопируйте ссылку и добавьте её в VPN-клиент.</div>
        <button className="pill-btn" onClick={() => copyLink()}>
          <Copy /> Скопировать ссылку
        </button>
      </div>
    )

  if (view === 'steps' && app)
    return (
      <div className="stack lg">
        {header('Шаги установки', () => setView('overview'))}
        <div className="inst-sel">
          <span className="inst-sel-ico">{platIcon(current, 20)}</span>
          <span className="grow">{current.name}</span>
          <button className="link-btn" onClick={() => setPickOpen(true)}>
            Сменить
          </button>
        </div>
        {current.apps.length > 1 && (
          <div className="chips" role="tablist" aria-label="Приложение" style={{ margin: '-6px 0 0' }}>
            {current.apps.map((a, i) => (
              <button key={a.name} role="tab" aria-selected={a === app} className={a === app ? 'chip on' : 'chip'} onClick={() => setAppIdx(i)}>
                {a.name}
                {a.featured && current.apps.filter((x) => x.featured).length === 1 ? ' · рекомендуем' : ''}
              </button>
            ))}
          </div>
        )}
        <ol className="inst-steps">
          {app.steps.map((s, i) => {
            const color = hexColor(s.color) || stepColors[i % stepColors.length]
            return (
              <li key={i} className="glass inst-step">
                <div className="inst-step-head">
                  <span className="inst-step-ico" style={{ color, background: `color-mix(in srgb, ${color} 18%, transparent)` }}>
                    {stepIcon(s)}
                  </span>
                  <h3>{s.title}</h3>
                </div>
                {s.text && <div className="small muted guide-text" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(s.text) }} />}
                {s.buttons.length > 0 && (
                  <div className="inst-btns">
                    {s.buttons.map((btn, j) => (
                      <button key={j} className={btn.kind === 'add' ? 'inst-btn main' : 'inst-btn'} onClick={() => press(btn)}>
                        {(rw && btn.icon && rw.svgLibrary?.[btn.icon] && <Svg lib={rw.svgLibrary} k={btn.icon} size={18} />) ||
                          (btn.kind === 'add' ? <Plus /> : btn.kind === 'copy' ? <Copy /> : <Download />)}
                        {btn.label}
                      </button>
                    ))}
                  </div>
                )}
              </li>
            )
          })}
        </ol>
        {config.install.note && <p className="small muted">{config.install.note}</p>}
        <PlatformSheet open={pickOpen} onClose={() => setPickOpen(false)} platforms={platforms} current={current.key} icon={platIcon} onPick={(k) => setPlatform(k)} />
      </div>
    )

  return (
    <div className="stack lg">
      {header('Установка и настройка', () => nav(-1))}
      <section className="glass inst-card">
        <p className="inst-label">{isDetected ? 'Ваше устройство' : 'Выбранное устройство'}</p>
        <div className="inst-device">
          <span className="inst-device-ico">{platIcon(current, 30)}</span>
          <span className="inst-device-name">{current.name}</span>
        </div>
        <button className="pill-btn" onClick={() => setView('steps')}>
          Продолжить
          <Chevron />
        </button>
        <button className="link-btn inst-other" onClick={() => setPickOpen(true)}>
          Другое устройство?
        </button>
      </section>

      <section className="glass inst-card">
        <div className="inst-share-head">
          <span className="link-ico">
            <LinkIcon />
          </span>
          <div className="grow">
            <h3>Поделиться ссылкой</h3>
            <p className="tiny muted">Наведите камеру второго телефона, планшета или телевизора</p>
          </div>
        </div>
        {qr && (
          <div className="inst-qr">
            <img src={qr} alt="QR-код ссылки подписки" />
            {b.logo && <img className="inst-qr-logo" src={b.logo} alt="" />}
          </div>
        )}
        <div className="inst-share-btns">
          <button className="pill-btn filled" onClick={share}>
            Поделиться
          </button>
          <button className="pill-btn" onClick={() => copyLink()}>
            <Copy />
            Скопировать ссылку
          </button>
        </div>
      </section>
      <PlatformSheet
        open={pickOpen}
        onClose={() => setPickOpen(false)}
        platforms={platforms}
        current={current.key}
        icon={platIcon}
        onPick={(k) => {
          setPlatform(k)
          setView('steps')
        }}
      />
    </div>
  )
}

function PlatformSheet({ open, onClose, platforms, current, icon, onPick }: { open: boolean; onClose: () => void; platforms: GPlatform[]; current: string; icon: (p: GPlatform, size?: number) => ReactNode; onPick: (k: string) => void }) {
  return (
    <Sheet open={open} onClose={onClose} title="Выберите устройство" icon={<Phone />}>
      <div className="inst-grid">
        {platforms.map((p) => (
          <button
            key={p.key}
            className={p.key === current ? 'inst-tile on' : 'inst-tile'}
            onClick={() => {
              haptic('light')
              onPick(p.key)
              onClose()
            }}
          >
            {icon(p, 28)}
            <span>{p.name}</span>
          </button>
        ))}
      </div>
    </Sheet>
  )
}
