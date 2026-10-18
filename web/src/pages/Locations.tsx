import { get, type Location } from '../lib/api'
import { Globe } from '../ui/icons'
import { ErrorBox, PageLoader, useLoad } from '../ui/kit'

// Regional-indicator flag from a two-letter country code; unknown codes get no flag.
function flag(cc: string) {
  if (!/^[A-Z]{2}$/.test(cc)) return ''
  return String.fromCodePoint(...[...cc].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65))
}

/** Read-only list of servers (enabled in admin → Общие → Локации). The VPN app picks the server itself. */
export default function Locations() {
  const list = useLoad(() => get<Location[]>('/api/locations'))
  if (list.loading && !list.data) return <PageLoader />
  if (list.error) return <ErrorBox text={list.error} retry={list.reload} />
  const items = list.data || []
  return (
    <div className="stack lg">
      <div>
        <h1>Локации</h1>
        <p className="small muted" style={{ marginTop: 4 }}>
          Серверы, доступные в подписке. Выбрать сервер можно в приложении VPN.
        </p>
      </div>
      {items.length === 0 ? (
        <div className="card empty-state">
          <Globe />
          <p className="small">Список серверов пока пуст.</p>
        </div>
      ) : (
        <div className="stack">
          {items.map((l, i) => (
            <div key={i} className="menu-row loc-row">
              <span className="loc-flag" aria-hidden="true">
                {flag(l.country) || <Globe className="menu-ico" />}
              </span>
              <span className="grow">{l.name}</span>
              <span className={l.online ? 'loc-status on' : 'loc-status'}>{l.online ? 'Работает' : 'Недоступен'}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
