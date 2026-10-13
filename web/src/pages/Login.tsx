import { useEffect, useRef, useState } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { post, type Session } from '../lib/api'
import { useApp } from '../lib/app'
import { Backdrop, Brand, HeroSpace } from '../ui/shell'
import { Field, useAction } from '../ui/kit'
import { inTelegramClient, launchInfo, reopen } from '../lib/tg'

declare global {
  interface Window {
    onTelegramAuth?: (u: Record<string, string | number>) => void
  }
}

function TelegramWidget({ bot, onAuth }: { bot: string; onAuth: (u: Record<string, string | number>) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!ref.current || !bot) return
    window.onTelegramAuth = onAuth
    const s = document.createElement('script')
    s.src = 'https://telegram.org/js/telegram-widget.js?22'
    s.async = true
    s.setAttribute('data-telegram-login', bot)
    s.setAttribute('data-size', 'large')
    s.setAttribute('data-radius', '12')
    s.setAttribute('data-onauth', 'onTelegramAuth(user)')
    s.setAttribute('data-request-access', 'write')
    ref.current.innerHTML = ''
    ref.current.appendChild(s)
  }, [bot, onAuth])
  return <div ref={ref} style={{ minHeight: 44, display: 'flex', justifyContent: 'center' }} />
}

export default function Login() {
  const { config, me, signIn, tgError } = useApp()
  const [params] = useSearchParams()
  const nav = useNavigate()
  const ret = params.get('return') || '/'
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [step, setStep] = useState<'start' | 'code'>('start')
  const { busy, run } = useAction()

  if (me) return <Navigate to={ret} replace />

  const done = async (s: Session) => {
    await signIn(s)
    nav(ret, { replace: true })
  }

  const sendCode = () =>
    run(async () => {
      await post('/api/auth/email/send', { email })
      setStep('code')
    })

  const verify = () =>
    run(async () => {
      const s = await post<Session>('/api/auth/email/verify', { email, code })
      await done(s)
    })

  const oauth = (provider: string) =>
    run(async () => {
      const r = await post<{ url: string }>(`/api/auth/oauth/${provider}/start`, { return_to: ret })
      window.location.href = r.url
    })

  const b = config.branding
  const tgClient = inTelegramClient()
  return (
    <>
      <Backdrop b={b} />
      <main className="app" style={{ paddingBottom: 40 }}>
        <Brand />
        <div style={{ marginBottom: 20 }}>
          <HeroSpace />
        </div>
        <div className="card stack lg">
          <div className="stack">
            <h1>Вход в кабинет</h1>
            {b.login_text && <p className="muted">{b.login_text}</p>}
          </div>

          {tgClient && (
            <div className="stack">
              <p className="small" style={{ margin: 0 }}>
                {tgError ? `Не получилось войти через Telegram: ${tgError}.` : 'Telegram не передал данные для входа — закройте приложение и откройте его снова кнопкой «Открыть» в боте.'}
              </p>
              <p className="tiny muted" style={{ margin: 0 }}>
                {launchInfo()}
              </p>
              <button className="btn block" onClick={() => reopen(config.bot_username)}>
                Открыть заново
              </button>
            </div>
          )}

          {!tgClient && config.auth.telegram && config.bot_username && (
            <div className="stack">
              <TelegramWidget bot={config.bot_username} onAuth={(u) => run(async () => done(await post<Session>('/api/auth/telegram/widget', u)))} />
              <a className="btn secondary block" href={`https://t.me/${config.bot_username}`}>
                Открыть бота в Telegram
              </a>
            </div>
          )}

          {config.auth.email && (
            <div className="stack">
              {step === 'start' ? (
                <form
                  className="stack"
                  onSubmit={(e) => {
                    e.preventDefault()
                    sendCode()
                  }}
                >
                  <Field label="Почта" hint="Пришлём код для входа. Пароль не нужен.">
                    <input className="input" type="email" inputMode="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" />
                  </Field>
                  <button className="btn block" disabled={busy || !email}>
                    Получить код
                  </button>
                </form>
              ) : (
                <form
                  className="stack"
                  onSubmit={(e) => {
                    e.preventDefault()
                    verify()
                  }}
                >
                  <Field label={`Код из письма на ${email}`}>
                    <input className="input code-input" inputMode="numeric" autoComplete="one-time-code" maxLength={6} autoFocus value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} />
                  </Field>
                  <button className="btn block" disabled={busy || code.length !== 6}>
                    Войти
                  </button>
                  <button type="button" className="btn ghost block" onClick={() => setStep('start')}>
                    Изменить почту
                  </button>
                </form>
              )}
            </div>
          )}

          {(config.auth.yandex || config.auth.vk) && (
            <div className="row">
              {config.auth.yandex && (
                <button className="btn secondary grow" disabled={busy} onClick={() => oauth('yandex')}>
                  Яндекс ID
                </button>
              )}
              {config.auth.vk && (
                <button className="btn secondary grow" disabled={busy} onClick={() => oauth('vk')}>
                  VK ID
                </button>
              )}
            </div>
          )}
        </div>
      </main>
    </>
  )
}
