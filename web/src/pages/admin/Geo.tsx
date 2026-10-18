import { Suspense, lazy, useEffect, useMemo, useState } from 'react'
import { get, put } from '../../lib/api'
import { dateTime, relative } from '../../lib/format'
import { Kpi } from '../../ui/charts'
import { Spinner, Toggle, useAction, useLoad } from '../../ui/kit'
import { Panel } from '../../ui/admin'
import { Globe } from '../../ui/icons'
import type { GeoPoint } from '../../ui/geomap'

// Geography of subscription requests: a globe/map plus "country → cities" lists. Used on the Statistics →
// Geography tab (all customers) and in a customer's card (one customer).

const GeoMap = lazy(() => import('../../ui/geomap'))

export interface GeoStatus {
  enabled: boolean
  ready: boolean
  db_date?: string
  db_error?: string
  synced_at?: string
  sync_error?: string
  retention_days: number
}
export interface GeoCity {
  city: string
  region?: string
  count: number
  lat?: number
  lon?: number
}
export interface GeoCountry {
  code: string
  count: number
  customers: number
  cities: GeoCity[]
}
export interface GeoReport {
  total: number
  customers: number
  countries: GeoCountry[]
  status: GeoStatus
}
export interface GeoRequest {
  at: string
  ip: string
  country: string
  region?: string
  city?: string
  user_agent?: string
}
export interface GeoDevice {
  name: string
  platform?: string
  ip: string
  country: string
  region?: string
  city?: string
  at: string
}
export interface CustomerGeoReport extends GeoReport {
  recent: GeoRequest[]
  devices: GeoDevice[]
}

let names: Intl.DisplayNames | null = null
export function countryName(code: string) {
  if (!code) return 'Не определено'
  try {
    names ??= new Intl.DisplayNames(['ru'], { type: 'region' })
    return names.of(code) || code
  } catch {
    return code
  }
}

export function place(city?: string, region?: string, country?: string) {
  const parts = [city, region && region !== city ? region : '', country ? countryName(country) : ''].filter(Boolean)
  return parts.join(', ') || 'Не определено'
}

function useMapData(countries: GeoCountry[]) {
  return useMemo(() => {
    const byCountry: Record<string, number> = {}
    const points: GeoPoint[] = []
    for (const c of countries) {
      if (c.code) byCountry[c.code] = c.count
      for (const ct of c.cities) {
        if (ct.lat == null || ct.lon == null) continue
        points.push({ lat: ct.lat, lon: ct.lon, count: ct.count, label: place(ct.city, ct.region, c.code) })
      }
    }
    points.sort((a, b) => b.count - a.count)
    return { byCountry, points: points.slice(0, 300) }
  }, [countries])
}

function StatusNote({ s }: { s: GeoStatus }) {
  if (!s.enabled) return <p className="small muted">Сбор географии выключен. Включите его ниже, и через несколько минут здесь появятся данные.</p>
  if (!s.ready && !s.db_error) return <p className="small muted">База геолокации скачивается (около 130 МБ), это занимает пару минут. Страница обновится сама, когда она будет готова.</p>
  return (
    <>
      {s.db_error && <p className="small err">{s.db_error}</p>}
      {s.sync_error && (
        <p className="soft-err" title={s.sync_error}>
          Не удалось обновить историю из панели — показаны сохранённые данные. Повторим автоматически.
        </p>
      )}
    </>
  )
}

function Lists({ countries, total }: { countries: GeoCountry[]; total: number }) {
  if (!total) return <p className="muted small">За выбранный период запросов нет.</p>
  return (
    <div className="geo-lists">
      {countries.map((c) => (
        <div key={c.code || '—'} className="geo-country">
          <span className="geo-pill">
            {countryName(c.code)}: {c.count}
          </span>
          {c.cities.slice(0, 8).map((ct) => (
            <span key={`${ct.city}|${ct.region}`} className="geo-pill sub">
              {ct.city}
              {ct.region && ct.region !== ct.city ? `, ${ct.region}` : ''}: {ct.count}
            </span>
          ))}
          {c.cities.length > 8 && <span className="geo-more muted tiny">и ещё {c.cities.length - 8}</span>}
        </div>
      ))}
    </div>
  )
}

/** The map, the numbers and the lists for one report. */
export function GeoView({ report }: { report: GeoReport }) {
  const { byCountry, points } = useMapData(report.countries)
  const cities = report.countries.reduce((a, c) => a + c.cities.length, 0)
  return (
    <div className="stack">
      <StatusNote s={report.status} />
      <div className="kpis">
        <Kpi label="Запросов подписки" value={report.total} />
        <Kpi label="Клиентов" value={report.customers} />
        <Kpi label="Стран" value={report.countries.filter((c) => c.code).length} />
        <Kpi label="Городов" value={cities} />
      </div>
      <section className="card">
        <div className="card-title">
          <h3 style={{ margin: 0 }}>География запросов</h3>
          <span className="badge accent">{report.total}</span>
        </div>
        <Suspense
          fallback={
            <div className="center" style={{ height: 260 }}>
              <Spinner />
            </div>
          }
        >
          <GeoMap countries={byCountry} points={points} />
        </Suspense>
        <Lists countries={report.countries} total={report.total} />
        <p className="tiny muted" style={{ margin: '14px 0 0' }}>
          Это место, откуда приложения клиентов обращаются за подпиской, а не точное положение человека: если подписка обновляется через другой VPN, на карте будет его страна.
        </p>
      </section>
    </div>
  )
}

/** Statistics → Geography. */
export function GeoSettings({ onSaved }: { onSaved: () => void }) {
  const data = useLoad(() => get<{ enabled: boolean; retention_days: number }>('/api/admin/settings/geo'))
  const [v, setV] = useState<{ enabled: boolean; retention_days: number } | null>(null)
  const { busy, run } = useAction()
  useEffect(() => {
    if (data.data) setV({ ...data.data })
  }, [data.data])
  if (!v) return null
  const dirty = JSON.stringify(v) !== JSON.stringify(data.data)
  return (
    <section className="card stack">
      <h3 style={{ margin: 0 }}>Сбор географии</h3>
      <Toggle checked={v.enabled} onChange={(on) => setV({ ...v, enabled: on })} label="Собирать запросы подписки из панели" hint="Раз в 5 минут бот копирует из Remnawave, с каких IP клиенты обновляли подписку, и определяет страну и город." />
      <div className="field">
        <label>Хранить запросы, дней</label>
        <input className="input num" type="number" min={7} max={730} style={{ maxWidth: 140 }} value={v.retention_days} onChange={(e) => setV({ ...v, retention_days: Number(e.target.value) })} />
        <span className="hint">IP-адреса — персональные данные, поэтому старые записи удаляются сами.</span>
      </div>
      <div>
        <button
          className="btn small"
          disabled={busy || !dirty}
          onClick={() =>
            run(async () => {
              data.setData(await put('/api/admin/settings/geo', v))
              onSaved()
            }, 'Сохранено')
          }
        >
          Сохранить
        </button>
      </div>
      <p className="tiny muted" style={{ margin: 0 }}>
        Геолокация по IP: <a href="https://db-ip.com" target="_blank" rel="noreferrer">IP Geolocation by DB-IP</a> (лицензия CC BY 4.0). Контуры стран: Natural Earth.
      </p>
    </section>
  )
}

/** The block in a customer's card. */
export function CustomerGeo({ id }: { id: number }) {
  const data = useLoad(() => get<CustomerGeoReport>(`/api/admin/customers/${id}/geo`), [id])
  const [all, setAll] = useState(false)
  const d = data.data
  const { byCountry, points } = useMapData(d?.countries || [])
  if (data.error) return null
  return (
    <Panel icon={Globe} tone="blue" title="География подключений" count={d?.total}>
      {!d ? (
        <div className="center" style={{ padding: 20 }}>
          <Spinner />
        </div>
      ) : (
        <>
          <StatusNote s={d.status} />
          {d.total > 0 && (
            <Suspense
              fallback={
                <div className="center" style={{ height: 200 }}>
                  <Spinner />
                </div>
              }
            >
              <GeoMap countries={byCountry} points={points} compact />
            </Suspense>
          )}
          <Lists countries={d.countries} total={d.total} />
          {d.devices.length > 0 && (
            <div className="stack" style={{ gap: 6 }}>
              <div className="small muted">Устройства: последний адрес</div>
              {d.devices.map((x, i) => (
                <div key={i} className="geo-row">
                  <div className="grow" style={{ minWidth: 0 }}>
                    <div className="ellipsis">{x.name}</div>
                    <div className="tiny muted ellipsis">{place(x.city, x.region, x.country)}</div>
                  </div>
                  <div className="tiny muted num" style={{ textAlign: 'right' }}>
                    <div>{x.ip}</div>
                    <div>{relative(x.at)}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
          {d.recent.length > 0 && (
            <div className="stack" style={{ gap: 6 }}>
              <button className="link-btn" onClick={() => setAll(!all)}>
                {all ? 'Скрыть последние запросы' : `Последние запросы (${d.recent.length})`}
              </button>
              {all &&
                d.recent.map((r, i) => (
                  <div key={i} className="geo-row">
                    <div className="grow" style={{ minWidth: 0 }}>
                      <div className="ellipsis">{place(r.city, r.region, r.country)}</div>
                      <div className="tiny muted ellipsis">{r.user_agent || '—'}</div>
                    </div>
                    <div className="tiny muted num" style={{ textAlign: 'right' }}>
                      <div>{r.ip}</div>
                      <div>{dateTime(r.at)}</div>
                    </div>
                  </div>
                ))}
            </div>
          )}
        </>
      )}
    </Panel>
  )
}
