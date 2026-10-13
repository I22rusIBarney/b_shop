import { useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { del, get, post, type Device, type Subscription } from '../lib/api'
import { useApp } from '../lib/app'
import { bytes, date, plural, relative, timeLeft } from '../lib/format'
import { haptic, openLink } from '../lib/tg'
import { Copy, Download, Phone, Plus, Renew, Trash } from '../ui/icons'
import { ErrorBox, PageLoader, Sheet, copy, useAction, useLoad, useToast } from '../ui/kit'

export default function Home() {
  const { me, config, refreshMe } = useApp()
  const [params, setParams] = useSearchParams()
  const subs = useLoad(() => get<Subscription[]>('/api/subscriptions'))
  const [sel, setSel] = useState<number | null>(null)
  const { busy, run } = useAction()
  const nav = useNavigate()

  const trial = () =>
    run(async () => {
      await post('/api/trial')
      haptic('success')
      await Promise.all([subs.reload(), refreshMe()])
    }, 'Пробный период активирован')

  useEffect(() => {
    if (params.get('trial') === '1' && me?.trial_available) {
      setParams({}, { replace: true })
      trial()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (subs.loading && !subs.data) return <PageLoader />
  if (subs.error) return <ErrorBox text={subs.error} retry={subs.reload} />
  const list = subs.data || []
  const b = config.branding

  const banner = b.banner ? (
    <div className="card" style={{ padding: 0, overflow: 'hidden', marginBottom: 14, cursor: b.banner_link ? 'pointer' : 'default' }} onClick={() => b.banner_link && openLink(b.banner_link)}>
      {b.banner.includes('#video') ? <video src={b.banner} autoPlay muted loop playsInline /> : <img src={b.banner} alt="" />}
    </div>
  ) : null

  if (list.length === 0)
    return (
      <div className="stack lg">
        {banner}
        <div className="card stack lg">
          <div className="stack">
            <h1>Подписки пока нет</h1>
            <p className="muted">Выберите тариф — подключение займёт пару минут, настройки придут сразу после оплаты.</p>
          </div>
          {me?.trial_available && (
            <button className="btn block" disabled={busy} onClick={trial}>
              Попробовать бесплатно — {config.trial.days} {plural(config.trial.days, 'день', 'дня', 'дней')}
            </button>
          )}
          <Link className={me?.trial_available ? 'btn secondary block' : 'btn block'} to="/buy">
            Выбрать тариф
          </Link>
        </div>
      </div>
    )

  const current = list.find((s) => s.id === sel) || list.find((s) => s.is_primary) || list[0]
  return (
    <div className="stack lg">
      {banner}
      {list.length > 1 || current ? (
        <div className="chips" role="tablist">
          {list.map((s) => (
            <button key={s.id} role="tab" aria-selected={s.id === current.id} className={s.id === current.id ? 'chip on' : 'chip'} onClick={() => setSel(s.id)}>
              {s.plan_name || s.title}
            </button>
          ))}
          <button className="chip" onClick={() => nav('/buy')} aria-label="Добавить подписку">
            <Plus style={{ width: 14, height: 14, verticalAlign: '-2px' }} /> Ещё
          </button>
        </div>
      ) : null}
      <Ticket sub={current} onChanged={subs.reload} />
    </div>
  )
}

function Ticket({ sub, onChanged }: { sub: Subscription; onChanged: () => void }) {
  const toast = useToast()
  const nav = useNavigate()
  const [devicesOpen, setDevicesOpen] = useState(false)
  const left = timeLeft(sub.expires_at)
  const limit = sub.traffic_limit_bytes
  const pct = limit > 0 ? Math.min(100, (sub.used_traffic_bytes / limit) * 100) : 0
  const devices = sub.device_limit > 0 ? sub.device_limit + sub.extra_devices : 0

  return (
    <>
      <section className={left.expired ? 'ticket expired' : 'ticket'} aria-label="Подписка">
        <div className="spread">
          <span className="plan">{sub.plan_name || sub.title}</span>
          {sub.kind === 'trial' && <span className="badge accent">Пробный</span>}
          {left.expired && <span className="badge bad">Истекла</span>}
        </div>
        <div className="left">
          <b className="num">{left.value}</b>
          <span>{left.expired ? 'дней осталось' : left.unit}</span>
        </div>
        <div className="until">{left.expired ? `закончилась ${date(sub.expires_at)}` : `до ${date(sub.expires_at)}`}</div>

        <div className="perf" />

        <div className="stats">
          <div>
            <div className="stat-label">Трафик</div>
            <div className="stat-value num">
              {bytes(sub.used_traffic_bytes)}
              {limit > 0 ? <span className="muted"> из {bytes(limit)}</span> : <span className="muted"> · безлимит</span>}
            </div>
            {limit > 0 && (
              <div className={pct > 90 ? 'meter bad' : pct > 75 ? 'meter warn' : 'meter'} style={{ marginTop: 6 }}>
                <i style={{ width: `${pct}%` }} />
              </div>
            )}
          </div>
          <button className="item-link" style={{ background: 'none', border: 0, padding: 0, textAlign: 'left' }} onClick={() => setDevicesOpen(true)}>
            <div className="stat-label">Устройства</div>
            <div className="stat-value num">
              {sub.devices_used}
              <span className="muted"> {devices ? `из ${devices}` : 'подключено'}</span>
            </div>
          </button>
        </div>

        {sub.subscription_url && (
          <div className="row" style={{ marginTop: 16 }}>
            <div className="mono-box grow ellipsis">{sub.subscription_url.replace(/^https?:\/\//, '')}</div>
            <button
              className="icon-btn"
              aria-label="Скопировать ссылку подписки"
              onClick={async () => {
                if (await copy(sub.subscription_url)) {
                  haptic('success')
                  toast('Ссылка скопирована')
                }
              }}
            >
              <Copy />
            </button>
          </div>
        )}
        <p className="tiny muted" style={{ marginTop: 10 }}>
          {sub.online_at ? `Последнее подключение ${relative(sub.online_at)}` : 'Ещё не подключались'}
        </p>
      </section>

      <div className="actions">
        <button className="btn secondary" disabled={!sub.can_renew} onClick={() => nav(`/buy?sub=${sub.id}&plan=${sub.plan_id}`)}>
          <Renew />
          Продлить
        </button>
        <Link className="btn secondary" to={`/install/${sub.id}`}>
          <Download />
          Установить
        </Link>
        <button className="btn secondary" onClick={() => setDevicesOpen(true)}>
          <Phone />
          Устройства
        </button>
      </div>

      <Devices sub={sub} open={devicesOpen} onClose={() => setDevicesOpen(false)} onChanged={onChanged} />
    </>
  )
}

function Devices({ sub, open, onClose, onChanged }: { sub: Subscription; open: boolean; onClose: () => void; onChanged: () => void }) {
  const [list, setList] = useState<Device[] | null>(null)
  const { busy, run } = useAction()
  useEffect(() => {
    if (!open) return
    setList(null)
    get<Device[]>(`/api/subscriptions/${sub.id}/devices`).then(setList, () => setList([]))
  }, [open, sub.id])

  const remove = (hwid?: string) =>
    run(async () => {
      await del(`/api/subscriptions/${sub.id}/devices${hwid ? `?hwid=${encodeURIComponent(hwid)}` : ''}`)
      setList((l) => (hwid ? (l || []).filter((d) => d.hwid !== hwid) : []))
      onChanged()
    }, hwid ? 'Устройство отключено' : 'Все устройства отключены')

  return (
    <Sheet open={open} onClose={onClose} title="Устройства">
      <p className="small muted" style={{ marginBottom: 12 }}>
        Удалите старое устройство, чтобы освободить место для нового. Удалённое устройство сможет подключиться снова, если хватит лимита.
      </p>
      {list === null ? (
        <div className="center" style={{ padding: 20 }}>
          <span className="spinner" />
        </div>
      ) : list.length === 0 ? (
        <div className="empty">Подключённых устройств нет</div>
      ) : (
        <div className="list">
          {list.map((d) => (
            <div className="item" key={d.hwid}>
              <Phone style={{ width: 22, height: 22, flex: 'none', color: 'var(--muted)' }} />
              <div className="grow">
                <div className="ellipsis">{d.deviceModel || d.platform || 'Устройство'}</div>
                <div className="tiny muted ellipsis">
                  {[d.platform, d.osVersion].filter(Boolean).join(' ')} · {relative(d.updatedAt)}
                </div>
              </div>
              <button className="icon-btn" aria-label="Удалить устройство" disabled={busy} onClick={() => remove(d.hwid)}>
                <Trash />
              </button>
            </div>
          ))}
        </div>
      )}
      {list && list.length > 1 && (
        <button className="btn danger block" style={{ marginTop: 14 }} disabled={busy} onClick={() => remove()}>
          Удалить все устройства
        </button>
      )}
    </Sheet>
  )
}
