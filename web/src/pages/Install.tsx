import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import QRCode from 'qrcode'
import DOMPurify from 'dompurify'
import { createHappCryptoLink } from '@kastov/cryptohapp'
import { get, type Subscription } from '../lib/api'
import { useApp } from '../lib/app'
import { haptic, openLink } from '../lib/tg'
import { Back, Copy } from '../ui/icons'
import { ErrorBox, PageLoader, copy, useLoad, useToast } from '../ui/kit'

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

const platformOrder = ['ios', 'android', 'windows', 'macos', 'linux', 'androidTV', 'appleTV']
const manualPlatforms: [string, string][] = [
  ['ios', 'iPhone'],
  ['android', 'Android'],
  ['windows', 'Windows'],
  ['macos', 'macOS'],
  ['linux', 'Linux'],
  ['tv', 'Телевизор'],
]

function detect(): string {
  const ua = navigator.userAgent
  if (/iPhone|iPad|iPod/i.test(ua)) return 'ios'
  if (/Android/i.test(ua)) return /TV|AFT|BRAVIA/i.test(ua) ? 'androidTV' : 'android'
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

function Svg({ lib, k, color, size = 20 }: { lib?: Record<string, string>; k?: string; color?: string; size?: number }) {
  const html = useMemo(() => {
    const raw = k && lib?.[k]
    return raw ? DOMPurify.sanitize(raw, { USE_PROFILES: { svg: true, svgFilters: true } }) : ''
  }, [lib, k])
  if (!html) return null
  return <span aria-hidden="true" style={{ display: 'inline-flex', width: size, height: size, color: color && color.startsWith('#') ? color : 'var(--accent)', flex: 'none' }} className="svg-slot" dangerouslySetInnerHTML={{ __html: html }} />
}

export default function Install() {
  const { id } = useParams()
  const nav = useNavigate()
  const toast = useToast()
  const { config } = useApp()
  const subs = useLoad(() => get<Subscription[]>('/api/subscriptions'))
  const guide = useLoad(() => get<Guide>(`/api/install-guide?subscription_id=${id || ''}`), [id])
  const [platform, setPlatform] = useState(detect)
  const [appIdx, setAppIdx] = useState(0)
  const [qr, setQr] = useState('')
  const sub = subs.data?.find((s) => s.id === Number(id))
  const url = sub?.subscription_url || ''

  useEffect(() => {
    if (url) QRCode.toDataURL(url, { margin: 1, width: 400 }).then(setQr, () => setQr(''))
  }, [url])
  useEffect(() => setAppIdx(0), [platform])

  if ((subs.loading && !subs.data) || (guide.loading && !guide.data)) return <PageLoader />
  if (subs.error) return <ErrorBox text={subs.error} retry={subs.reload} />
  if (!sub) return <ErrorBox text="Подписка не найдена" />

  const copyLink = async (text = url) => {
    if (await copy(text)) {
      haptic('success')
      toast('Ссылка скопирована')
    }
  }
  const share = async () => {
    if (navigator.share) {
      try {
        await navigator.share({ url, title: config.branding.title })
        return
      } catch {
        /* cancelled */
      }
    }
    copyLink()
  }

  const rw = guide.data?.source === 'remnawave' ? guide.data.config : undefined
  const header = (
    <div className="row">
      <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
        <Back />
      </button>
      <h1>Подключение</h1>
    </div>
  )
  const otherDevice = (
    <div className="card stack center">
      <h3>Другое устройство</h3>
      <p className="small muted">Отсканируйте QR-код камерой телевизора или второго телефона.</p>
      {qr && <img className="qr" src={qr} alt="QR-код ссылки подписки" />}
      <div className="row">
        <div className="mono-box grow ellipsis">{url.replace(/^https?:\/\//, '')}</div>
        <button className="icon-btn" onClick={() => copyLink()} aria-label="Скопировать ссылку">
          <Copy />
        </button>
      </div>
      <button className="btn secondary block" onClick={share}>
        Поделиться ссылкой
      </button>
    </div>
  )

  if (rw) {
    const keys = platformOrder.filter((k) => rw.platforms[k]?.apps?.length).concat(Object.keys(rw.platforms).filter((k) => !platformOrder.includes(k) && rw.platforms[k]?.apps?.length))
    const current = keys.includes(platform) ? platform : keys[0]
    const p = rw.platforms[current]
    const apps = p ? [...p.apps].sort((a, b) => Number(b.featured) - Number(a.featured)) : []
    const app = apps[Math.min(appIdx, apps.length - 1)]
    const run = (b: RwButton) => {
      const link = fill(b.link, url, sub.rw_username || '')
      if (b.type === 'copyButton') copyLink(link)
      else openLink(link)
    }
    return (
      <div className="stack lg">
        {header}
        <div className="chips" role="tablist" aria-label="Устройство">
          {keys.map((k) => (
            <button key={k} role="tab" aria-selected={k === current} className={k === current ? 'chip on' : 'chip'} onClick={() => setPlatform(k)}>
              {tr(rw.platforms[k].displayName) || k}
            </button>
          ))}
        </div>
        {apps.length > 1 && (
          <div className="chips" role="tablist" aria-label="Приложение" style={{ marginTop: -8 }}>
            {apps.map((a, i) => (
              <button key={a.name} role="tab" aria-selected={a === app} className={a === app ? 'chip on' : 'chip'} onClick={() => setAppIdx(i)}>
                {a.name}
                {a.featured ? ' ★' : ''}
              </button>
            ))}
          </div>
        )}
        {app ? (
          <ol className="stack" style={{ listStyle: 'none', padding: 0, margin: 0 }}>
            {app.blocks.map((b, i) => (
              <li key={i} className="card stack">
                <div className="row">
                  <Svg lib={rw.svgLibrary} k={b.svgIconKey} color={b.svgIconColor} size={22} />
                  <h3 className="grow">{tr(b.title)}</h3>
                </div>
                {tr(b.description) && <div className="small muted guide-text" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(fill(tr(b.description), url, sub.rw_username || '')) }} />}
                {b.buttons.length > 0 && (
                  <div className="stack" style={{ gap: 8 }}>
                    {b.buttons.map((btn, j) => (
                      <button key={j} className={btn.type === 'subscriptionLink' ? 'btn block' : 'btn secondary block'} onClick={() => run(btn)}>
                        <Svg lib={rw.svgLibrary} k={btn.svgIconKey} size={18} color={btn.type === 'subscriptionLink' ? 'currentColor' : undefined} />
                        {tr(btn.text)}
                      </button>
                    ))}
                  </div>
                )}
              </li>
            ))}
          </ol>
        ) : (
          <div className="card empty">Для этой платформы инструкции пока нет.</div>
        )}
        {otherDevice}
      </div>
    )
  }

  // Manual list from shop settings.
  const manual = config.install.apps
  const mp = manualPlatforms.some(([k]) => k === platform) ? platform : platform === 'androidTV' || platform === 'appleTV' ? 'tv' : 'android'
  const apps = manual.filter((a) => a.platform === mp)
  return (
    <div className="stack lg">
      {header}
      <div className="chips" role="tablist" aria-label="Устройство">
        {manualPlatforms.map(([k, name]) => (
          <button key={k} role="tab" aria-selected={k === mp} className={k === mp ? 'chip on' : 'chip'} onClick={() => setPlatform(k)}>
            {name}
          </button>
        ))}
      </div>
      <ol className="stack" style={{ listStyle: 'none', padding: 0, margin: 0 }}>
        <li className="card stack">
          <h3>1. Установите приложение</h3>
          {apps.length === 0 ? (
            <p className="small muted">Подойдёт любой клиент с поддержкой подписок Xray (VLESS).</p>
          ) : (
            apps.map((a) => (
              <button key={a.name} className="btn secondary block" onClick={() => a.store_url && openLink(a.store_url)} disabled={!a.store_url}>
                Скачать {a.name}
              </button>
            ))
          )}
        </li>
        <li className="card stack">
          <h3>2. Добавьте подписку</h3>
          {apps.filter((a) => a.deep_link).length > 0 ? (
            apps
              .filter((a) => a.deep_link)
              .map((a) => (
                <button key={a.name} className="btn block" onClick={() => openLink(fill(a.deep_link.replace('{url}', encodeURIComponent(url)).replace('{url_b64}', btoa(url)), url, sub.rw_username || ''))}>
                  Добавить в {a.name}
                </button>
              ))
          ) : (
            <p className="small muted">Скопируйте ссылку ниже и добавьте её в приложении через «Импорт из буфера».</p>
          )}
        </li>
        <li className="card stack">
          <h3>3. Включите VPN</h3>
          <p className="small muted">Нажмите кнопку подключения в приложении. Если что-то не работает — напишите в поддержку.</p>
        </li>
      </ol>
      {otherDevice}
      {config.install.note && <p className="small muted">{config.install.note}</p>}
    </div>
  )
}
