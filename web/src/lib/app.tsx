import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { get, getToken, post, setToken, type Me, type PublicConfig, type Session } from './api'
import { applyBranding } from './theme'
import * as tg from './tg'

interface AppState {
  config: PublicConfig
  me: Me | null
  refreshMe: () => Promise<void>
  signIn: (s: Session) => Promise<void>
  signOut: () => Promise<void>
  /** Why the automatic Telegram sign-in didn't work (shown on the login screen). */
  tgError: string
}

const Ctx = createContext<AppState | null>(null)

export function useApp(): AppState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useApp outside provider')
  return v
}

type Boot = { state: 'loading' } | { state: 'error'; message: string } | { state: 'ready'; config: PublicConfig }

export function AppProvider({ children, fallback, failed }: { children: ReactNode; fallback: ReactNode; failed: (msg: string, retry: () => void) => ReactNode }) {
  const [boot, setBoot] = useState<Boot>({ state: 'loading' })
  const [me, setMe] = useState<Me | null>(null)
  const [tgError, setTgError] = useState('')

  const refreshMe = useCallback(async () => {
    if (!getToken()) {
      setMe(null)
      return
    }
    try {
      setMe(await get<Me>('/api/me'))
    } catch (e: any) {
      if (e?.status === 401) setMe(null)
      else throw e
    }
  }, [])

  const start = useCallback(async () => {
    setBoot({ state: 'loading' })
    try {
      // Inside Telegram wait for the SDK: after a reload the launch data is only available through it.
      await tg.whenSDK()
      tg.ready()
      const config = await get<PublicConfig>('/api/config')
      applyBranding(config.branding)
      if (tg.inTelegram()) {
        // Inside Telegram the launch data is the source of truth for who is signed in.
        let err = ''
        for (let i = 0; i < 2; i++) {
          try {
            const s = await post<Session>('/api/auth/telegram/webapp', { init_data: tg.initData() })
            // Never wipe a working session with an empty answer.
            if (s.token) {
              setToken(s.token)
              tg.cloudSetToken(s.token)
            }
            err = ''
            break
          } catch (e: any) {
            err = e?.message || 'ошибка сети'
            // A rejected launch won't get better on retry; a network hiccup may.
            if (e?.status && e.status < 500) break
            await new Promise((r) => setTimeout(r, 800))
          }
        }
        setTgError(err)
      }
      if (tg.inTelegramClient()) {
        // No stored session (WebView storage cleared) — take the one saved in Telegram CloudStorage.
        if (!getToken()) {
          const saved = await tg.cloudGetToken()
          if (saved) setToken(saved)
        }
      }
      await refreshMe()
      setBoot({ state: 'ready', config })
    } catch (e: any) {
      setBoot({ state: 'error', message: e?.message || 'Сервис недоступен' })
    }
  }, [refreshMe])

  useEffect(() => {
    start()
  }, [start])

  const signIn = useCallback(
    async (s: Session) => {
      if (s.token) {
        setToken(s.token)
        tg.cloudSetToken(s.token)
      }
      await refreshMe()
    },
    [refreshMe],
  )

  const signOut = useCallback(async () => {
    try {
      await post('/api/auth/logout')
    } catch {
      /* ignore */
    }
    setToken(null)
    tg.cloudSetToken(null)
    setMe(null)
  }, [])

  if (boot.state === 'loading') return <>{fallback}</>
  if (boot.state === 'error') return <>{failed(boot.message, start)}</>
  return <Ctx.Provider value={{ config: boot.config, me, refreshMe, signIn, signOut, tgError }}>{children}</Ctx.Provider>
}
