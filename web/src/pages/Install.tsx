import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import QRCode from 'qrcode'
import { get, type Subscription } from '../lib/api'
import { useApp } from '../lib/app'
import { haptic, openLink } from '../lib/tg'
import { Back, Copy } from '../ui/icons'
import { ErrorBox, PageLoader, copy, useLoad, useToast } from '../ui/kit'

const platforms: { id: string; name: string }[] = [
  { id: 'ios', name: 'iPhone' },
  { id: 'android', name: 'Android' },
  { id: 'windows', name: 'Windows' },
  { id: 'macos', name: 'macOS' },
  { id: 'linux', name: 'Linux' },
  { id: 'tv', name: 'Телевизор' },
]

function detect(): string {
  const ua = navigator.userAgent
  if (/iPhone|iPad|iPod/i.test(ua)) return 'ios'
  if (/Android/i.test(ua)) return /TV|AFT/i.test(ua) ? 'tv' : 'android'
  if (/Mac OS X/i.test(ua)) return 'macos'
  if (/Windows/i.test(ua)) return 'windows'
  if (/Linux/i.test(ua)) return 'linux'
  return 'android'
}

function b64(s: string) {
  return btoa(unescape(encodeURIComponent(s)))
}

export default function Install() {
  const { id } = useParams()
  const nav = useNavigate()
  const toast = useToast()
  const { config } = useApp()
  const subs = useLoad(() => get<Subscription[]>('/api/subscriptions'))
  const [platform, setPlatform] = useState(detect)
  const [qr, setQr] = useState('')
  const sub = subs.data?.find((s) => s.id === Number(id))
  const url = sub?.subscription_url || ''

  useEffect(() => {
    if (url) QRCode.toDataURL(url, { margin: 1, width: 400 }).then(setQr, () => setQr(''))
  }, [url])

  const apps = useMemo(() => config.install.apps.filter((a) => a.platform === platform), [config.install.apps, platform])

  if (subs.loading && !subs.data) return <PageLoader />
  if (subs.error) return <ErrorBox text={subs.error} retry={subs.reload} />
  if (!sub) return <ErrorBox text="Подписка не найдена" />

  const copyLink = async () => {
    if (await copy(url)) {
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

  return (
    <div className="stack lg">
      <div className="row">
        <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
          <Back />
        </button>
        <h1>Подключение</h1>
      </div>

      <div className="chips" role="tablist" aria-label="Устройство">
        {platforms.map((p) => (
          <button key={p.id} role="tab" aria-selected={p.id === platform} className={p.id === platform ? 'chip on' : 'chip'} onClick={() => setPlatform(p.id)}>
            {p.name}
          </button>
        ))}
      </div>

      <ol className="stack" style={{ listStyle: 'none', padding: 0, margin: 0 }}>
        <li className="card stack">
          <h3>1. Установите приложение</h3>
          {apps.length === 0 ? (
            <p className="small muted">Подойдёт любой клиент с поддержкой подписок Xray (VLESS). Список приложений администратор добавит в настройках.</p>
          ) : (
            <div className="stack">
              {apps.map((a) => (
                <button key={a.name} className="btn secondary block" onClick={() => a.store_url && openLink(a.store_url)} disabled={!a.store_url}>
                  Скачать {a.name}
                </button>
              ))}
            </div>
          )}
        </li>
        <li className="card stack">
          <h3>2. Добавьте подписку</h3>
          {apps.some((a) => a.deep_link) ? (
            apps
              .filter((a) => a.deep_link)
              .map((a) => (
                <button key={a.name} className="btn block" onClick={() => openLink(a.deep_link.replace('{url}', encodeURIComponent(url)).replace('{url_b64}', b64(url)))}>
                  Добавить в {a.name}
                </button>
              ))
          ) : (
            <p className="small muted">Скопируйте ссылку и вставьте её в приложении через «Добавить подписку» или «Импорт из буфера».</p>
          )}
          <div className="row">
            <div className="mono-box grow ellipsis">{url.replace(/^https?:\/\//, '')}</div>
            <button className="icon-btn" onClick={copyLink} aria-label="Скопировать ссылку">
              <Copy />
            </button>
          </div>
        </li>
        <li className="card stack">
          <h3>3. Включите VPN</h3>
          <p className="small muted">Нажмите кнопку подключения в приложении. Если что-то не работает — напишите в поддержку, поможем.</p>
        </li>
      </ol>

      <div className="card stack center">
        <h3>Другое устройство</h3>
        <p className="small muted">Отсканируйте QR-код камерой телевизора или второго телефона.</p>
        {qr && <img className="qr" src={qr} alt="QR-код ссылки подписки" />}
        <button className="btn secondary block" onClick={share}>
          Поделиться ссылкой
        </button>
      </div>
      {config.install.note && <p className="small muted">{config.install.note}</p>}
    </div>
  )
}
