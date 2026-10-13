import { useApp } from '../lib/app'
import { openLink } from '../lib/tg'
import { Chat } from '../ui/icons'

// Tickets with the AI assistant arrive in stage 4; until then support goes through the configured link.
export default function Support() {
  const { config } = useApp()
  const support = config.links.find((l) => l.kind === 'support')
  return (
    <div className="card stack lg center">
      <div style={{ display: 'grid', placeItems: 'center', color: 'var(--accent)' }}>
        <Chat style={{ width: 40, height: 40 }} />
      </div>
      <h1>Поддержка</h1>
      <p className="muted">Опишите, что случилось, — ответим и поможем с подключением.</p>
      {support ? (
        <button className="btn block" onClick={() => openLink(support.url)}>
          Написать в поддержку
        </button>
      ) : (
        <p className="small muted">Контакт поддержки появится здесь, когда администратор его добавит.</p>
      )}
    </div>
  )
}
