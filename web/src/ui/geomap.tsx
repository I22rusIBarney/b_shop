import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { geoDistance, geoGraticule10, geoNaturalEarth1, geoOrthographic, geoPath } from 'd3-geo'
import { feature } from 'topojson-client'
import type { Feature, FeatureCollection, Geometry } from 'geojson'
import type { Topology } from 'topojson-specification'
import numeric from './geo/numeric.json'

// A globe and a flat map of where requests come from. Country outlines are Natural Earth (110m, public domain)
// from the world-atlas package, loaded as a separate chunk the first time a map is shown.

export interface GeoPoint {
  lat: number
  lon: number
  count: number
  label: string
}

type Country = Feature<Geometry, { name?: string }> & { iso?: string }

let atlas: Promise<Country[]> | null = null
function loadAtlas(): Promise<Country[]> {
  atlas ??= import('world-atlas/countries-110m.json').then((m) => {
    const topo = (m.default ?? m) as unknown as Topology
    const fc = feature(topo, topo.objects.countries) as unknown as FeatureCollection<Geometry, { name?: string }>
    const byNum = numeric as unknown as Record<string, string | Record<string, string>>
    const byName = (byNum._name || {}) as Record<string, string>
    return fc.features.map((f) => {
      const id = f.id != null ? String(f.id).padStart(3, '0') : ''
      const iso = (typeof byNum[id] === 'string' ? (byNum[id] as string) : '') || byName[f.properties?.name || ''] || ''
      return Object.assign(f, { iso }) as Country
    })
  })
  return atlas
}

const clamp = (v: number, a: number, b: number) => Math.min(b, Math.max(a, v))

export default function GeoMap({ countries, points, compact }: { countries: Record<string, number>; points: GeoPoint[]; compact?: boolean }) {
  const [mode, setMode] = useState<'globe' | 'map'>('globe')
  const [features, setFeatures] = useState<Country[]>([])
  const [failed, setFailed] = useState(false)
  const [rot, setRot] = useState<[number, number]>([-60, -30]) // [lambda, phi] of the projection
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState<[number, number]>([0, 0])
  const [sel, setSel] = useState<GeoPoint | null>(null)
  const [width, setWidth] = useState(320)
  const box = useRef<HTMLDivElement>(null)
  const drag = useRef<{ x: number; y: number; id: number } | null>(null)

  useEffect(() => {
    let live = true
    loadAtlas().then(
      (f) => live && setFeatures(f),
      () => live && setFailed(true),
    )
    return () => {
      live = false
    }
  }, [])

  useEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setWidth(Math.max(240, Math.round(el.clientWidth))))
    ro.observe(el)
    setWidth(Math.max(240, Math.round(el.clientWidth)))
    return () => ro.disconnect()
  }, [])

  const top = useMemo(() => points.reduce<GeoPoint | null>((b, p) => (!b || p.count > b.count ? p : b), null), [points])
  const topKey = top ? `${top.lat},${top.lon}` : ''
  const faceTop = useCallback(() => {
    setRot(top ? [-top.lon, -clamp(top.lat, -60, 60)] : [-40, -25])
    setPan([0, 0])
    setZoom(1)
  }, [top])
  // Turn the globe to the busiest place whenever the data changes (not on every re-render).
  useEffect(() => {
    faceTop()
    setSel(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topKey])

  const H = mode === 'globe' ? Math.min(width, compact ? 300 : 460) : Math.round(width * 0.54)
  const maxCountry = Math.max(1, ...Object.values(countries))
  const maxPoint = Math.max(1, ...points.map((p) => p.count))

  const { proj, path } = useMemo(() => {
    if (mode === 'globe') {
      const r = (Math.min(width, H) / 2 - 8) * zoom
      const p = geoOrthographic().scale(r).translate([width / 2, H / 2]).rotate([rot[0], rot[1]]).clipAngle(90)
      return { proj: p, path: geoPath(p) }
    }
    const p = geoNaturalEarth1().fitExtent([[4, 4], [width - 4, H - 4]], { type: 'Sphere' })
    p.scale(p.scale() * zoom).translate([width / 2 + pan[0], H / 2 + pan[1]])
    return { proj: p, path: geoPath(p) }
  }, [mode, width, H, zoom, rot, pan])

  const fillOf = (iso?: string) => {
    const n = iso ? countries[iso] : 0
    if (!n) return 'color-mix(in srgb, var(--text) 9%, var(--surface-solid))'
    const t = Math.log1p(n) / Math.log1p(maxCountry)
    return `color-mix(in srgb, var(--accent) ${Math.round(22 + 78 * t)}%, var(--surface-solid))`
  }

  const onDown = (e: React.PointerEvent) => {
    drag.current = { x: e.clientX, y: e.clientY, id: e.pointerId }
    ;(e.currentTarget as Element).setPointerCapture?.(e.pointerId)
  }
  const onMove = (e: React.PointerEvent) => {
    const d = drag.current
    if (!d) return
    const dx = e.clientX - d.x
    const dy = e.clientY - d.y
    d.x = e.clientX
    d.y = e.clientY
    if (mode === 'globe') {
      const k = 90 / ((Math.min(width, H) / 2 - 8) * zoom) // dragging by one radius turns the globe by 90°
      setRot(([l, f]) => [l + dx * k, clamp(f - dy * k, -90, 90)])
    } else if (zoom > 1) {
      const lim = (width * zoom) / 2
      setPan(([x, y]) => [clamp(x + dx, -lim, lim), clamp(y + dy, -H * zoom / 2, H * zoom / 2)])
    }
  }
  const onUp = () => {
    drag.current = null
  }

  const center: [number, number] = [-rot[0], -rot[1]]
  const dots = points
    .map((p) => {
      if (mode === 'globe' && geoDistance([p.lon, p.lat], center) > Math.PI / 2 - 0.02) return null
      const xy = proj([p.lon, p.lat])
      return xy ? { p, x: xy[0], y: xy[1], r: 2.6 + 7.4 * Math.sqrt(p.count / maxPoint) } : null
    })
    .filter((d): d is { p: GeoPoint; x: number; y: number; r: number } => !!d)
    .sort((a, b) => b.r - a.r)

  const sphere = path({ type: 'Sphere' })
  const grat = path(geoGraticule10())

  return (
    <div className="geomap" ref={box}>
      <div className="geomap-bar">
        <div className="seg" role="tablist">
          <button className={mode === 'globe' ? 'on' : ''} onClick={() => (setMode('globe'), setZoom(1), setPan([0, 0]))}>
            Глобус
          </button>
          <button className={mode === 'map' ? 'on' : ''} onClick={() => (setMode('map'), setZoom(1), setPan([0, 0]))}>
            Карта
          </button>
        </div>
      </div>
      <div className="geomap-stage" style={{ height: H }}>
        {failed ? (
          <div className="center muted small" style={{ height: '100%' }}>
            Не удалось загрузить контуры стран
          </div>
        ) : (
          <svg
            viewBox={`0 0 ${width} ${H}`}
            width={width}
            height={H}
            role="img"
            aria-label="Карта запросов подписки"
            style={{ touchAction: mode === 'globe' || zoom > 1 ? 'none' : 'pan-y', cursor: drag.current ? 'grabbing' : 'grab' }}
            onPointerDown={onDown}
            onPointerMove={onMove}
            onPointerUp={onUp}
            onPointerCancel={onUp}
          >
            <defs>
              <clipPath id="geo-clip">
                <rect width={width} height={H} rx="14" />
              </clipPath>
            </defs>
            <g clipPath="url(#geo-clip)">
              {sphere && <path d={sphere} className="geo-sea" />}
              {grat && <path d={grat} className="geo-grat" />}
              {features.map((f, i) => {
                const d = path(f)
                return d ? <path key={i} d={d} className="geo-land" style={{ fill: fillOf(f.iso) }} /> : null
              })}
              {sphere && mode === 'globe' && <path d={sphere} className="geo-rim" />}
              <g className="geo-dots">
                {dots.map(({ p, x, y, r }) => (
                  <circle key={`${p.lat},${p.lon},${p.label}`} cx={x} cy={y} r={r} onClick={() => setSel(p)} onPointerDown={(e) => e.stopPropagation()}>
                    <title>{`${p.label}: ${p.count}`}</title>
                  </circle>
                ))}
              </g>
            </g>
          </svg>
        )}
        <div className="geomap-zoom">
          <button onClick={() => setZoom((z) => clamp(+(z * 1.5).toFixed(2), 1, 8))} aria-label="Приблизить">
            +
          </button>
          <button onClick={() => setZoom((z) => clamp(+(z / 1.5).toFixed(2), 1, 8))} aria-label="Отдалить">
            −
          </button>
          <button onClick={faceTop} aria-label="Вернуть вид" title="Вернуть вид">
            ◎
          </button>
        </div>
      </div>
      <div className="geomap-legend">
        <span>мало</span>
        <i />
        <span>много</span>
      </div>
      {sel && (
        <div className="geomap-sel small">
          <b>{sel.label}</b> · {sel.count} {plural(sel.count)}
        </div>
      )}
    </div>
  )
}

function plural(n: number) {
  const a = n % 100
  const b = n % 10
  if (a > 10 && a < 20) return 'запросов'
  if (b === 1) return 'запрос'
  if (b >= 2 && b <= 4) return 'запроса'
  return 'запросов'
}
