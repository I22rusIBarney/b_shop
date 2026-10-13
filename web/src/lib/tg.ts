// Telegram Mini App helpers. initData is read from the SDK or straight from the launch URL,
// so login works even if telegram-web-app.js is slow to load.

declare global {
  interface Window {
    Telegram?: { WebApp?: any }
  }
}

const launchHash = window.location.hash

function webApp(): any | undefined {
  return window.Telegram?.WebApp
}

export function initData(): string {
  const sdk = webApp()?.initData
  if (sdk) return sdk
  const params = new URLSearchParams(launchHash.slice(1))
  const fromHash = params.get('tgWebAppData')
  if (fromHash) return fromHash
  // After a reload inside Telegram the launch hash is gone; the SDK keeps the launch params here.
  try {
    const saved = JSON.parse(sessionStorage.getItem('__telegram__initParams') || '{}')
    if (saved?.tgWebAppData) return saved.tgWebAppData
  } catch {
    /* ignore */
  }
  return ''
}

/** Opened inside Telegram (even if the launch data is missing). */
export function inTelegramClient(): boolean {
  if (initData() !== '') return true
  const p = webApp()?.platform
  return !!p && p !== 'unknown'
}

/** Resolves once telegram-web-app.js has loaded (or failed), at most after `ms`. */
export function whenSDK(ms = 3000): Promise<void> {
  if (webApp()) return Promise.resolve()
  return new Promise((resolve) => {
    const started = Date.now()
    const tick = () => {
      if (webApp() || Date.now() - started > ms) return resolve()
      const tag = document.querySelector<HTMLScriptElement>('script[src*="telegram-web-app.js"]')
      if (tag && (tag as any).dataset.failed) return resolve()
      setTimeout(tick, 40)
    }
    tick()
  })
}

/** Reopen the Mini App from the bot (fresh launch data). */
export function reopen(bot: string) {
  const w = webApp()
  if (w && bot) {
    try {
      w.openTelegramLink(`https://t.me/${bot}`)
      w.close()
      return
    } catch {
      /* fall through */
    }
  }
  window.location.reload()
}

// ---- session backup in Telegram CloudStorage ----
// Telegram keeps it per user and bot on its servers, so the session survives a cleared WebView storage.
const CLOUD_KEY = 'bshop_session'

function cloud(): any | undefined {
  const w = webApp()
  try {
    return w?.isVersionAtLeast?.('6.9') ? w.CloudStorage : undefined
  } catch {
    return undefined
  }
}

export function cloudGetToken(ms = 1500): Promise<string> {
  const c = cloud()
  if (!c) return Promise.resolve('')
  return new Promise((resolve) => {
    const t = setTimeout(() => resolve(''), ms)
    try {
      c.getItem(CLOUD_KEY, (err: unknown, v?: string) => {
        clearTimeout(t)
        resolve(!err && v ? v : '')
      })
    } catch {
      clearTimeout(t)
      resolve('')
    }
  })
}

export function cloudSetToken(tok: string | null) {
  const c = cloud()
  if (!c) return
  try {
    if (tok) c.setItem(CLOUD_KEY, tok)
    else c.removeItem(CLOUD_KEY)
  } catch {
    /* ignore */
  }
}

/** Short description of the launch for the login screen, to tell launch problems apart. */
export function launchInfo(): string {
  const w = webApp()
  return [w ? `${w.platform || '?'} ${w.version || ''}`.trim() : 'без SDK', initData() ? `данные ${initData().length} б` : 'без данных запуска'].join(' · ')
}

export function inTelegram(): boolean {
  return initData() !== ''
}

export function startParam(): string {
  const sdk = webApp()?.initDataUnsafe?.start_param
  if (sdk) return sdk
  const data = new URLSearchParams(initData())
  return data.get('start_param') || new URLSearchParams(window.location.search).get('startapp') || ''
}

export function ready() {
  const w = webApp()
  if (!w) return
  try {
    w.ready()
    w.expand()
    w.setHeaderColor?.('bg_color')
    w.disableVerticalSwipes?.()
    // Phones: open full screen like a native app (Telegram 8.0+).
    if (['ios', 'android'].includes(w.platform) && w.isVersionAtLeast?.('8.0')) w.requestFullscreen?.()
  } catch {
    /* old clients */
  }
}

/** Opens an external link: in Telegram via the client, otherwise in the same tab. */
export function openLink(url: string) {
  const w = webApp()
  if (w && inTelegram()) {
    if (url.startsWith('https://t.me/')) w.openTelegramLink(url)
    else w.openLink(url)
    return
  }
  window.location.href = url
}

/** Opens an app link (happ://, v2raytun://…). Telegram only opens http(s) links, so inside Telegram
 *  the link goes through the shop's /open page in the browser, which forwards it to the app. */
export function openAppLink(url: string) {
  if (/^https?:/i.test(url)) return openLink(url)
  if (webApp() && inTelegram()) {
    openLink(`${location.origin}/open?to=${encodeURIComponent(url)}`)
    return
  }
  window.location.href = url
}

export function haptic(kind: 'success' | 'error' | 'light' = 'light') {
  const h = webApp()?.HapticFeedback
  if (!h) return
  try {
    if (kind === 'light') h.impactOccurred('light')
    else h.notificationOccurred(kind)
  } catch {
    /* ignore */
  }
}
