import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { del, get, post, type Device, type Subscription } from '../lib/api'
import { useApp } from '../lib/app'
import { bytes, plural, relative, shortDate } from '../lib/format'
import { haptic, openLink } from '../lib/tg'
import { Calendar, Chevron, ChevronDown, Copy, Gear, Link as LinkIcon, Pencil, Phone, Plug, Swap, Trash } from '../ui/icons'
import { ErrorBox, PageLoader, Sheet, copy, useAction, useLoad, useToast } from '../ui/kit'
import { HeroSpace } from '../ui/shell'

function subTitle(_s: Subscription, i: number) {
  return `Подписка ${i + 1}`
}

export default function Home() {
  const { me, config, refreshMe } = useApp()
  const [params, setParams] = useSearchParams()
  const subs = useLoad(() => get<Subscription[]>('/api/subscriptions'))
  const [idx, setIdx] = useState(0)
  const [pickOpen, setPickOpen] = useState(false)
  const { busy, run } = useAction()
  const nav = useNavigate()
  const [dir, setDir] = useState(0)
  const touch = useRef<{ x: number; y: number } | null>(null)

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

  // Primary subscription first.
  const list = [...(subs.data || [])].sort((a, b) => Number(b.is_primary) - Number(a.is_primary))
  useEffect(() => {
    if (idx > 0 && idx >= list.length) setIdx(0)
  }, [idx, list.length])

  const go = (i: number) => {
    const n = Math.max(0, Math.min(i, list.length - 1))
    if (n === idx) return
    setDir(n > idx ? 1 : -1)
    setIdx(n)
    haptic('light')
  }
  const onTouchStart = (e: React.TouchEvent) => {
    touch.current = { x: e.touches[0].clientX, y: e.touches[0].clientY }
  }
  const onTouchEnd = (e: React.TouchEvent) => {
    const t = touch.current
    touch.current = null
    if (!t) return
    const dx = e.changedTouches[0].clientX - t.x
    const dy = e.changedTouches[0].clientY - t.y
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) go(idx + (dx < 0 ? 1 : -1))
  }

  if (subs.loading && !subs.data) return <PageLoader />
  if (subs.error) return <ErrorBox text={subs.error} retry={subs.reload} />
  const b = config.branding

  const banner = b.banner ? (
    <div className="glass" style={{ padding: 0, overflow: 'hidden', cursor: b.banner_link ? 'pointer' : 'default' }} onClick={() => b.banner_link && openLink(b.banner_link)}>
      {b.banner.includes('#video') ? <video src={b.banner} autoPlay muted loop playsInline /> : <img src={b.banner} alt="" />}
    </div>
  ) : null

  if (list.length === 0)
    return (
      <div className="home">
        <HeroSpace />
        {banner}
        <div className="glass sub-card stack lg">
          <div className="stack">
            <h2>Подписки пока нет</h2>
            <p className="muted">Выберите тариф — подключение займёт пару минут.</p>
          </div>
          {me?.trial_available && (
            <button className="pill-btn filled" disabled={busy} onClick={trial}>
              Попробовать бесплатно — {config.trial.days} {plural(config.trial.days, 'день', 'дня', 'дней')}
            </button>
          )}
          <Link className="pill-btn" to="/buy">
            Выбрать тариф
          </Link>
        </div>
      </div>
    )

  return (
    <div className="home">
      <div className="sub-picker-row">
        <button className="sub-picker" onClick={() => setPickOpen(true)}>
          {subTitle(list[idx], idx)}
          <ChevronDown />
        </button>
      </div>
      <HeroSpace />
      {banner}
      <div className="carousel-wrap">
        <div className="carousel" onTouchStart={onTouchStart} onTouchEnd={onTouchEnd}>
          {list[idx] && (
            <div className="slide" key={list[idx].id} style={{ ['--from' as any]: `${dir * 24}px` }}>
              <SubCard sub={list[idx]} onChanged={subs.reload} />
            </div>
          )}
        </div>
        {idx > 0 && (
          <button className="car-arrow left" onClick={() => go(idx - 1)} aria-label="Предыдущая подписка">
            <Chevron style={{ transform: 'rotate(180deg)' }} />
          </button>
        )}
        {idx < list.length - 1 && (
          <button className="car-arrow right" onClick={() => go(idx + 1)} aria-label="Следующая подписка">
            <Chevron />
          </button>
        )}
      </div>
      <div className="dots-row">
        <div className="dots">
          {list.map((s, i) => (
            <button key={s.id} className={i === idx ? 'dot on' : 'dot'} onClick={() => go(i)} aria-label={subTitle(s, i)} />
          ))}
          <button className="dot plus" onClick={() => nav('/buy')} aria-label="Новая подписка">
            +
          </button>
        </div>
      </div>

      <Sheet open={pickOpen} onClose={() => setPickOpen(false)} title="Подписки">
        <div className="list">
          {list.map((s, i) => (
            <button
              key={s.id}
              className="item item-link"
              style={{ background: 'none', border: 0, borderTop: i ? '1px solid var(--line)' : 0, width: '100%', textAlign: 'left' }}
              onClick={() => {
                setPickOpen(false)
                go(i)
              }}
            >
              <div className="grow">
                <div>{subTitle(s, i)}</div>
                <div className="tiny muted">
                  {s.plan_name || s.title} · до {shortDate(s.expires_at)}
                </div>
              </div>
              {i === idx && <span className="badge accent">открыта</span>}
            </button>
          ))}
        </div>
        <Link className="pill-btn" style={{ marginTop: 16 }} to="/buy">
          Новая подписка
        </Link>
      </Sheet>
    </div>
  )
}

function SubCard({ sub, onChanged }: { sub: Subscription; onChanged: () => void }) {
  const toast = useToast()
  const nav = useNavigate()
  const [devicesOpen, setDevicesOpen] = useState(false)
  const expired = new Date(sub.expires_at) < new Date()
  const limit = sub.traffic_limit_bytes
  const pct = limit > 0 ? Math.min(100, (sub.used_traffic_bytes / limit) * 100) : 0
  const devices = sub.device_limit > 0 ? sub.device_limit + sub.extra_devices : 0
  const url = sub.subscription_url

  return (
    <section className="glass sub-card" aria-label={sub.plan_name || sub.title}>
      <div className="sub-head">
        <span className="sub-name">{sub.plan_name || sub.title}</span>
        <span className={expired ? 'sub-date bad' : 'sub-date'}>
          {expired ? 'истекла ' : 'до '}
          <b>{shortDate(sub.expires_at)}</b>
          <Calendar />
        </span>
      </div>
      <div className="sub-meta">
        <button className="dev-pill" onClick={() => setDevicesOpen(true)}>
          {sub.devices_used}
          {devices ? `/${devices}` : ''} {plural(devices || sub.devices_used, 'устройство', 'устройства', 'устройств')}
          <Pencil />
        </button>
        {sub.kind === 'trial' && <span className="dev-pill muted-pill">Пробная</span>}
      </div>
      {limit > 0 && (
        <div className="traffic">
          <div className="spread tiny">
            <span className="muted">Трафик</span>
            <span className="num">
              {bytes(sub.used_traffic_bytes)} из {bytes(limit)}
            </span>
          </div>
          <div className={pct > 90 ? 'meter bad' : pct > 75 ? 'meter warn' : 'meter'}>
            <i style={{ width: `${pct}%` }} />
          </div>
        </div>
      )}

      {url && (
        <div className="link-row">
          <span className="link-ico">
            <LinkIcon />
          </span>
          <div className="grow" style={{ minWidth: 0 }}>
            <div className="link-label">Ссылка подписки</div>
            <div className="link-url">
              <span className="u">{url}</span>
              <span className="link-mask">••••••••</span>
            </div>
          </div>
          <button
            className="link-copy"
            aria-label="Скопировать ссылку подписки"
            onClick={async () => {
              if (await copy(url)) {
                haptic('success')
                toast('Ссылка скопирована')
              }
            }}
          >
            <Copy />
          </button>
        </div>
      )}

      <div className="sub-actions">
        <button className="pill-btn" disabled={!sub.can_renew} onClick={() => nav(`/buy?sub=${sub.id}&plan=${sub.plan_id}`)}>
          <Plug />
          Продлить
        </button>
        <Link className="pill-btn" to={`/install/${sub.id}`}>
          <Gear />
          Установить и настроить
        </Link>
        {sub.can_change_plan && (
          <button className="pill-btn" onClick={() => nav(`/buy?sub=${sub.id}&change=1`)}>
            <Swap />
            Сменить тариф
          </button>
        )}
      </div>
      {sub.online_at && <p className="tiny muted center" style={{ marginTop: 10 }}>Подключение {relative(sub.online_at)}</p>}

      <Devices sub={sub} open={devicesOpen} onClose={() => setDevicesOpen(false)} onChanged={onChanged} />
    </section>
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
