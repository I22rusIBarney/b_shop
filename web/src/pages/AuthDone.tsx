import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { setToken } from '../lib/api'
import { useApp } from '../lib/app'
import { PageLoader } from '../ui/kit'

/** Landing page after Yandex/VK redirects: the token arrives in the URL fragment. */
export default function AuthDone() {
  const { refreshMe } = useApp()
  const nav = useNavigate()
  const [error, setError] = useState('')

  useEffect(() => {
    const p = new URLSearchParams(window.location.hash.slice(1))
    const ret = p.get('return') || '/'
    history.replaceState(null, '', window.location.pathname)
    if (p.get('error')) {
      setError(p.get('error')!)
      return
    }
    if (p.get('token')) setToken(p.get('token'))
    refreshMe().then(() => nav(p.get('linked') ? '/profile' : ret, { replace: true }))
  }, [nav, refreshMe])

  if (error)
    return (
      <div className="page-center" style={{ padding: 24 }}>
        <div className="stack" style={{ maxWidth: 340 }}>
          <h2>Не получилось войти</h2>
          <p className="muted">{error}</p>
          <a className="btn" href="/login">
            Вернуться ко входу
          </a>
        </div>
      </div>
    )
  return <PageLoader />
}
