import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { del, get, post, put, type Plan, type Promo, type Redemption } from '../../lib/api'
import { date, dateTime } from '../../lib/format'
import { Copy, Plus, Trash } from '../../ui/icons'
import { ErrorBox, Field, Sheet, Spinner, Toggle, copy, useAction, useLoad, useToast } from '../../ui/kit'

const tabs = [
  { id: 'all', label: 'Все' },
  { id: 'active', label: 'Активные' },
  { id: 'inactive', label: 'Неактуальные' },
  { id: 'expired', label: 'Истёкшие' },
  { id: 'exhausted', label: 'Исчерпанные' },
] as const

const stateLabel: Record<string, [string, string]> = {
  active: ['активен', 'badge ok'],
  inactive: ['выключен', 'badge'],
  expired: ['истёк', 'badge bad'],
  exhausted: ['исчерпан', 'badge warn'],
  scheduled: ['запланирован', 'badge accent'],
}

const empty: Promo = {
  id: 0,
  code: '',
  kind: 'discount',
  value: 10,
  folder: '',
  description: '',
  is_active: true,
  max_uses: 0,
  valid_from: null,
  valid_until: null,
  plan_ids: [],
  new_customers_only: false,
  customer_id: null,
  reserve_hours: 0,
}

const valueText = (p: Pick<Promo, 'kind' | 'value'>) => (p.kind === 'discount' ? `−${p.value}%` : `+${p.value} дн.`)

export default function Promos() {
  const [tab, setTab] = useState<(typeof tabs)[number]['id']>('all')
  const [folder, setFolder] = useState<string | null>(null)
  const [q, setQ] = useState('')
  const [kind, setKind] = useState('')
  const [edit, setEdit] = useState<Promo | null>(null)
  const [usesOf, setUsesOf] = useState<Promo | null>(null)
  const toast = useToast()
  const params = new URLSearchParams()
  if (q) params.set('q', q)
  if (kind) params.set('kind', kind)
  if (folder !== null) params.set('folder', folder)
  const data = useLoad(() => get<{ items: Promo[]; folders: string[] }>(`/api/admin/promos?${params}`), [q, kind, folder])
  const plans = useLoad(() => get<Plan[]>('/api/admin/plans'))

  const items = useMemo(() => {
    const list = data.data?.items || []
    if (tab === 'all') return list
    if (tab === 'active') return list.filter((p) => p.state === 'active' || p.state === 'scheduled')
    return list.filter((p) => p.state === tab)
  }, [data.data, tab])

  if (data.error) return <ErrorBox text={data.error} retry={data.reload} />

  return (
    <>
      <div className="admin-head">
        <h1>Промокоды</h1>
        <button className="btn small" onClick={() => setEdit({ ...empty, folder: folder || '' })}>
          <Plus /> Новый промокод
        </button>
      </div>

      <div className="row wrap" style={{ marginBottom: 12 }}>
        <input className="input" placeholder="Поиск по коду или описанию" value={q} onChange={(e) => setQ(e.target.value)} style={{ maxWidth: 280 }} />
        <select className="input" value={kind} onChange={(e) => setKind(e.target.value)} style={{ maxWidth: 180 }} aria-label="Тип">
          <option value="">Все типы</option>
          <option value="discount">Скидка</option>
          <option value="days">Бонусные дни</option>
        </select>
      </div>
      <div className="chips">
        <button className={folder === null ? 'chip on' : 'chip'} onClick={() => setFolder(null)}>
          Все папки
        </button>
        <button className={folder === '' ? 'chip on' : 'chip'} onClick={() => setFolder('')}>
          Без папки
        </button>
        {(data.data?.folders || []).map((f) => (
          <button key={f} className={folder === f ? 'chip on' : 'chip'} onClick={() => setFolder(f)}>
            📁 {f}
          </button>
        ))}
      </div>
      <div className="chips">
        {tabs.map((t) => (
          <button key={t.id} className={tab === t.id ? 'chip on' : 'chip'} onClick={() => setTab(t.id)}>
            {t.label}
          </button>
        ))}
      </div>

      {!data.data ? (
        <div className="center" style={{ padding: 40 }}>
          <Spinner />
        </div>
      ) : items.length === 0 ? (
        <div className="card empty">
          <h3>Промокодов нет</h3>
          <p className="small">Скидка в процентах применяется к следующей оплате, бонусные дни — сразу к подписке.</p>
        </div>
      ) : (
        <div className="grid2">
          {items.map((p) => {
            const [label, cls] = stateLabel[p.state || 'active']
            return (
              <div key={p.id} className="card stack" style={{ opacity: p.state === 'active' ? 1 : 0.7 }}>
                <div className="spread">
                  <div className="row" style={{ gap: 8 }}>
                    <h3 className="num">{p.code}</h3>
                    <button
                      className="icon-btn"
                      aria-label="Скопировать код"
                      onClick={async () => {
                        if (await copy(p.code)) toast('Код скопирован')
                      }}
                    >
                      <Copy />
                    </button>
                  </div>
                  <span className={cls}>{label}</span>
                </div>
                <div className="row wrap small">
                  <span className="badge accent">{valueText(p)}</span>
                  <span className="muted">
                    использован {p.used}
                    {p.max_uses ? ` из ${p.max_uses}` : ''}
                    {p.reserved ? ` · ждут оплаты ${p.reserved}` : ''}
                  </span>
                </div>
                <div className="tiny muted">
                  {[
                    p.folder && `📁 ${p.folder}`,
                    p.valid_until && `до ${date(p.valid_until)}`,
                    p.new_customers_only && 'только новым',
                    p.customer_id && (
                      <Link key="c" to={`/admin/customers/${p.customer_id}`}>
                        персональный #{p.customer_id}
                      </Link>
                    ),
                    p.plan_ids.length > 0 && `тарифов: ${p.plan_ids.length}`,
                    p.description,
                  ]
                    .filter(Boolean)
                    .map((x, i) => (
                      <span key={i}>
                        {i > 0 && ' · '}
                        {x}
                      </span>
                    ))}
                </div>
                <div className="row wrap">
                  <button className="btn secondary small" onClick={() => setEdit(p)}>
                    Изменить
                  </button>
                  <button className="btn secondary small" onClick={() => setUsesOf(p)} disabled={!p.used && !p.reserved}>
                    Использования
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      <PromoEditor promo={edit} plans={plans.data || []} folders={data.data?.folders || []} onClose={() => setEdit(null)} onSaved={() => (setEdit(null), data.reload())} />
      <UsesSheet promo={usesOf} onClose={() => setUsesOf(null)} />
    </>
  )
}

const toLocal = (iso: string | null) => (iso ? new Date(new Date(iso).getTime() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 16) : '')
const fromLocal = (v: string) => (v ? new Date(v).toISOString() : null)

function PromoEditor({ promo, plans, folders, onClose, onSaved }: { promo: Promo | null; plans: Plan[]; folders: string[]; onClose: () => void; onSaved: () => void }) {
  const [p, setP] = useState<Promo>(empty)
  const { busy, run } = useAction()
  useEffect(() => {
    if (promo) setP({ ...promo, plan_ids: promo.plan_ids || [] })
  }, [promo])
  const set = <K extends keyof Promo>(k: K, v: Promo[K]) => setP((x) => ({ ...x, [k]: v }))

  const save = () =>
    run(async () => {
      const body = { ...p, code: p.code.trim().toUpperCase() }
      if (p.id) await put(`/api/admin/promos/${p.id}`, body)
      else await post('/api/admin/promos', body)
      onSaved()
    }, 'Промокод сохранён')
  const remove = () =>
    run(async () => {
      if (!confirm(`Удалить промокод ${p.code}? История использований тоже удалится.`)) return
      await del(`/api/admin/promos/${p.id}`)
      onSaved()
    }, 'Промокод удалён')
  const generate = () =>
    run(async () => {
      const r = await get<{ code: string }>(`/api/admin/promos/generate?prefix=${encodeURIComponent(p.code.replace(/[^A-Za-z0-9_-]/g, '').slice(0, 10))}`)
      set('code', r.code)
    })

  return (
    <Sheet open={!!promo} onClose={onClose} title={p.id ? `Промокод ${promo?.code}` : 'Новый промокод'}>
      <div className="stack">
        <Field label="Код" hint="Пусто или «Сгенерировать» — случайный код. Можно ввести префикс и нажать «Сгенерировать».">
          <div className="input-row">
            <input className="input num" value={p.code} onChange={(e) => set('code', e.target.value.toUpperCase())} placeholder="BOBR2026" />
            <button className="btn secondary" type="button" onClick={generate} disabled={busy}>
              Сгенерировать
            </button>
          </div>
        </Field>
        <div className="grid2" style={{ gap: 12, gridTemplateColumns: '1fr 1fr' }}>
          <Field label="Тип">
            <select className="input" value={p.kind} onChange={(e) => set('kind', e.target.value as Promo['kind'])}>
              <option value="discount">Скидка, %</option>
              <option value="days">Бонусные дни</option>
            </select>
          </Field>
          <Field label={p.kind === 'discount' ? 'Скидка, %' : 'Дней'}>
            <input className="input num" type="number" min={1} max={p.kind === 'discount' ? 100 : 3650} value={p.value} onChange={(e) => set('value', Number(e.target.value))} />
          </Field>
          <Field label="Лимит использований" hint="0 — без лимита">
            <input className="input num" type="number" min={0} value={p.max_uses} onChange={(e) => set('max_uses', Number(e.target.value))} />
          </Field>
          <Field label="Папка">
            <input className="input" list="promo-folders" value={p.folder} onChange={(e) => set('folder', e.target.value)} placeholder="Автоматизации" />
            <datalist id="promo-folders">
              {folders.map((f) => (
                <option key={f} value={f} />
              ))}
            </datalist>
          </Field>
          <Field label="Действует с">
            <input className="input" type="datetime-local" value={toLocal(p.valid_from)} onChange={(e) => set('valid_from', fromLocal(e.target.value))} />
          </Field>
          <Field label="Действует до">
            <input className="input" type="datetime-local" value={toLocal(p.valid_until)} onChange={(e) => set('valid_until', fromLocal(e.target.value))} />
          </Field>
        </div>
        {p.kind === 'discount' && (
          <Field label="Скидка ждёт оплату, часов" hint="Через столько часов неиспользованная скидка сгорит. 0 — пока действует код.">
            <input className="input num" type="number" min={0} value={p.reserve_hours} onChange={(e) => set('reserve_hours', Number(e.target.value))} />
          </Field>
        )}
        <Field label="Тарифы" hint="Не выбрано — любой тариф">
          <div className="chips" style={{ flexWrap: 'wrap', marginBottom: 0 }}>
            {plans
              .filter((x) => !x.is_trial)
              .map((x) => {
                const on = p.plan_ids.includes(x.id)
                return (
                  <button key={x.id} type="button" className={on ? 'chip on' : 'chip'} onClick={() => set('plan_ids', on ? p.plan_ids.filter((i) => i !== x.id) : [...p.plan_ids, x.id])}>
                    {x.name}
                  </button>
                )
              })}
          </div>
        </Field>
        <Field label="Описание (видно только в админке)">
          <input className="input" value={p.description} onChange={(e) => set('description', e.target.value)} />
        </Field>
        <Field label="Персональный для клиента #" hint="Пусто — для всех">
          <input className="input num" type="number" min={1} value={p.customer_id ?? ''} onChange={(e) => set('customer_id', e.target.value ? Number(e.target.value) : null)} />
        </Field>
        <Toggle checked={p.new_customers_only} onChange={(v) => set('new_customers_only', v)} label="Только для новых клиентов" hint="До первой оплаты" />
        <Toggle checked={p.is_active} onChange={(v) => set('is_active', v)} label="Включён" />
        <div className="row">
          <button className="btn" disabled={busy || p.value < 1} onClick={save}>
            Сохранить
          </button>
          {p.id > 0 && (
            <button className="btn ghost" disabled={busy} onClick={remove}>
              <Trash /> Удалить
            </button>
          )}
        </div>
      </div>
    </Sheet>
  )
}

function UsesSheet({ promo, onClose }: { promo: Promo | null; onClose: () => void }) {
  const data = useLoad(() => (promo ? get<Redemption[]>(`/api/admin/promos/${promo.id}/redemptions`) : Promise.resolve(undefined)), [promo?.id])
  return (
    <Sheet open={!!promo} onClose={onClose} title={`Использования ${promo?.code || ''}`}>
      {!data.data ? (
        <div className="center" style={{ padding: 20 }}>
          <Spinner />
        </div>
      ) : data.data.length === 0 ? (
        <div className="empty">Пока никто не использовал</div>
      ) : (
        <div className="list small">
          {data.data.map((r) => (
            <div className="item" key={r.id}>
              <Link className="grow" to={`/admin/customers/${r.customer_id}`} onClick={onClose}>
                {r.customer}
              </Link>
              <span className={r.status === 'used' ? 'badge ok' : 'badge warn'}>{r.status === 'used' ? 'использован' : 'ждёт оплаты'}</span>
              <span className="tiny muted">{dateTime(r.used_at || r.created_at)}</span>
            </div>
          ))}
        </div>
      )}
    </Sheet>
  )
}
