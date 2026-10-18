import { useState } from 'react'
import { del, get, post, put, type Banner } from '../../lib/api'
import { dateTime } from '../../lib/format'
import { BannerView, bannerIcons } from '../../ui/banners'
import { ChevronDown, Pencil, Plus, Trash } from '../../ui/icons'
import { ErrorBox, Field, PageLoader, Sheet, Toggle, useAction, useLoad } from '../../ui/kit'

const segments: Record<Banner['segment'], string> = {
  all: 'Все клиенты',
  active: 'С активной подпиской',
  trial: 'На пробном периоде',
  expired: 'Подписка истекла',
  no_sub: 'Без подписки',
}

const blank: Banner = {
  id: 0, kind: '', title: '', subtitle: '', icon: 'info', segment: 'all', button_text: '', button_url: '',
  starts_at: null, ends_at: null, is_active: true, dismissible: true, sort: 0,
}

// <input type="datetime-local"> works in local time without a zone.
const toLocal = (iso: string | null) => {
  if (!iso) return ''
  const d = new Date(iso)
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}
const fromLocal = (v: string) => (v ? new Date(v).toISOString() : null)

export default function Banners() {
  const list = useLoad(() => get<Banner[]>('/api/admin/banners'))
  const [edit, setEdit] = useState<Banner | null>(null)
  const { busy, run } = useAction()

  if (list.loading && !list.data) return <PageLoader />
  if (list.error) return <ErrorBox text={list.error} retry={list.reload} />
  const items = list.data || []

  const toggle = (b: Banner, v: boolean) => run(async () => {
    await put(`/api/admin/banners/${b.id}`, { ...b, is_active: v })
    await list.reload()
  })
  const move = (i: number, d: number) => run(async () => {
    const ids = items.map((b) => b.id)
    ;[ids[i], ids[i + d]] = [ids[i + d], ids[i]]
    await post('/api/admin/banners/reorder', { ids })
    await list.reload()
  })
  const remove = (b: Banner) => {
    if (!confirm(`Удалить баннер «${b.title}»?`)) return
    run(async () => {
      await del(`/api/admin/banners/${b.id}`)
      await list.reload()
    }, 'Баннер удалён')
  }

  return (
    <>
      <div className="admin-head">
        <h1>Баннеры кабинета</h1>
        <button className="btn small" onClick={() => setEdit({ ...blank })}>
          <Plus /> Создать
        </button>
      </div>
      <p className="small muted" style={{ marginBottom: 14 }}>
        Показываются на главной кабинета над карточкой подписки. Системные баннеры появляются только у тех, кому они нужны.
      </p>

      <div className="stack">
        {items.map((b, i) => (
          <div key={b.id} className={b.is_active ? 'card banner-row' : 'card banner-row off'}>
            <div className="banner-order">
              <button className="icon-btn" aria-label="Выше" disabled={busy || i === 0} onClick={() => move(i, -1)}>
                <ChevronDown style={{ transform: 'rotate(180deg)' }} />
              </button>
              <button className="icon-btn" aria-label="Ниже" disabled={busy || i === items.length - 1} onClick={() => move(i, 1)}>
                <ChevronDown />
              </button>
            </div>
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="row wrap" style={{ gap: 6 }}>
                <b>{b.title}</b>
                {b.kind && <span className="badge">системный</span>}
                <span className="badge">{segments[b.segment]}</span>
              </div>
              <div className="tiny muted" style={{ marginTop: 4 }}>
                {b.kind === 'link_email'
                  ? 'Показывается клиентам без привязанного email'
                  : b.kind === 'link_telegram'
                    ? 'Показывается клиентам без привязанного Telegram'
                    : b.subtitle || '—'}
                {(b.starts_at || b.ends_at) && ` · ${b.starts_at ? 'с ' + dateTime(b.starts_at) : ''} ${b.ends_at ? 'до ' + dateTime(b.ends_at) : ''}`}
              </div>
            </div>
            <button className="icon-btn" aria-label="Изменить" onClick={() => setEdit({ ...b })}>
              <Pencil />
            </button>
            {!b.kind && (
              <button className="icon-btn" aria-label="Удалить" disabled={busy} onClick={() => remove(b)}>
                <Trash />
              </button>
            )}
            <Toggle checked={b.is_active} onChange={(v) => toggle(b, v)} label="" disabled={busy} />
          </div>
        ))}
      </div>

      {edit && <BannerForm b={edit} onClose={() => setEdit(null)} onSaved={list.reload} />}
    </>
  )
}

function BannerForm({ b: start, onClose, onSaved }: { b: Banner; onClose: () => void; onSaved: () => Promise<void> }) {
  const [b, setB] = useState(start)
  const { busy, run } = useAction()
  const set = <K extends keyof Banner>(k: K, v: Banner[K]) => setB((x) => ({ ...x, [k]: v }))
  const system = !!b.kind

  const save = () =>
    run(async () => {
      if (b.id) await put(`/api/admin/banners/${b.id}`, b)
      else await post('/api/admin/banners', b)
      await onSaved()
      onClose()
    }, 'Сохранено')

  return (
    <Sheet open onClose={onClose} title={b.id ? 'Баннер' : 'Новый баннер'}>
      <form
        className="stack"
        onSubmit={(e) => {
          e.preventDefault()
          save()
        }}
      >
        <Field label="Заголовок">
          <input className="input" value={b.title} maxLength={80} onChange={(e) => set('title', e.target.value)} required />
        </Field>
        <Field label="Подзаголовок">
          <input className="input" value={b.subtitle} maxLength={200} onChange={(e) => set('subtitle', e.target.value)} />
        </Field>
        <div className="form-grid">
          <Field label="Иконка">
            <select className="input" value={b.icon} onChange={(e) => set('icon', e.target.value)}>
              {Object.entries(bannerIcons).map(([k, v]) => (
                <option key={k} value={k}>
                  {v.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Кому показывать">
            <select className="input" value={b.segment} onChange={(e) => set('segment', e.target.value as Banner['segment'])}>
              {Object.entries(segments).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Текст кнопки" hint="Пусто — без кнопки, баннер кликабелен целиком">
            <input className="input" value={b.button_text} maxLength={30} onChange={(e) => set('button_text', e.target.value)} placeholder="напр. Подробнее" />
          </Field>
          {!system && (
            <Field label="Ссылка" hint="/buy, /referral, https://… или tg://…">
              <input className="input" value={b.button_url} onChange={(e) => set('button_url', e.target.value)} placeholder="/buy" />
            </Field>
          )}
          <Field label="Показывать с" hint="Пусто — сразу">
            <input className="input" type="datetime-local" value={toLocal(b.starts_at)} onChange={(e) => set('starts_at', fromLocal(e.target.value))} />
          </Field>
          <Field label="Показывать до" hint="Пусто — бессрочно">
            <input className="input" type="datetime-local" value={toLocal(b.ends_at)} onChange={(e) => set('ends_at', fromLocal(e.target.value))} />
          </Field>
        </div>
        {system && <p className="small muted">Системный баннер: кнопка открывает привязку способа входа, ссылка не нужна.</p>}
        <Toggle checked={b.is_active} onChange={(v) => set('is_active', v)} label="Активен" />
        <Toggle checked={b.dismissible} onChange={(v) => set('dismissible', v)} label="Клиент может скрыть" hint="Крестик в углу; скрытый баннер больше не показывается этому клиенту" />
        <div className="field">
          <label>Предпросмотр</label>
          <BannerView b={{ ...b, title: b.title || 'Заголовок' }} onAction={() => {}} onClose={b.dismissible ? () => {} : undefined} />
        </div>
        <div className="row" style={{ justifyContent: 'flex-end', gap: 8 }}>
          <button type="button" className="btn secondary" onClick={onClose}>
            Отмена
          </button>
          <button className="btn" disabled={busy || !b.title.trim()}>
            Сохранить
          </button>
        </div>
      </form>
    </Sheet>
  )
}
