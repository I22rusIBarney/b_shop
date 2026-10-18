import { useState } from 'react'
import { useApp } from '../lib/app'
import { openLink } from '../lib/tg'
import { Send } from './icons'
import { useAction } from './kit'

/** Full-screen "subscribe to our channel" step (admin → Ссылки → Обязательная подписка). */
export function ChannelGate() {
  const { me, refreshMe } = useApp()
  const { busy, run } = useAction()
  const [tried, setTried] = useState(false)
  const missing = me?.missing_channels || []
  if (!missing.length) return null
  const check = () =>
    run(async () => {
      await refreshMe()
      setTried(true)
    })
  return (
    <div className="gate" role="dialog" aria-modal="true" aria-labelledby="gate-title">
      <div className="glass gate-card stack lg">
        <span className="gate-ico" aria-hidden="true">
          <Send />
        </span>
        <div className="stack" style={{ gap: 6 }}>
          <h2 id="gate-title">{missing.length > 1 ? 'Подпишитесь на наши каналы' : 'Подпишитесь на наш канал'}</h2>
          <p className="small muted">Там новости сервиса, статус серверов и акции. После подписки нажмите «Я подписался».</p>
        </div>
        <div className="stack">
          {missing.map((c) => (
            <button key={c.chat_id} className="pill-btn" onClick={() => openLink(c.url)}>
              <Send /> {c.title}
            </button>
          ))}
        </div>
        <button className="btn block" disabled={busy} onClick={check}>
          Я подписался
        </button>
        {tried && <p className="small err">Подписка пока не видна. Подпишитесь и нажмите ещё раз — иногда Telegram обновляет её несколько секунд.</p>}
      </div>
    </div>
  )
}
