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
  return params.get('tgWebAppData') || ''
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
