import { useEffect, useMemo, useRef, useState } from 'react'
import { Map as MapLibre, setWorkerUrl, type ExpressionSpecification, type GeoJSONSource, type StyleSpecification } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import workerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url'
import type { FeatureCollection, Geometry, Point } from 'geojson'
import GeoSvg, { loadAtlas, plural, type GeoPoint } from './geomap-svg'

export type { GeoPoint }

// Where subscription requests come from: a WebGL globe that turns into a street map as you zoom in
// (pinch, wheel or the buttons). Streets are OpenFreeMap vector tiles (OpenStreetMap data, no key, no account);
// country shading is Natural Earth from world-atlas. Points are city-level: DB-IP City Lite knows the city,
// not the house, so the closest zoom shows the city centre. Without WebGL the old SVG globe is used.

setWorkerUrl(workerUrl)

const STYLE_URL = 'https://tiles.openfreemap.org/styles/dark'
const MAX_ZOOM = 18

// Labels in Russian where OpenStreetMap has them.
const ruName: ExpressionSpecification = ['coalesce', ['get', 'name:ru'], ['get', 'name']]

let remoteStyle: Promise<StyleSpecification | null> | null = null
function loadStyle(): Promise<StyleSpecification | null> {
  remoteStyle ??= fetch(STYLE_URL, { signal: AbortSignal.timeout(8000) })
    .then((r) => (r.ok ? (r.json() as Promise<StyleSpecification>) : null))
    .catch(() => null)
  return remoteStyle
}

// Tiles unreachable: still a zoomable globe, just without streets.
const offlineStyle: StyleSpecification = { version: 8, sources: {}, layers: [{ id: 'background', type: 'background', paint: { 'background-color': '#14110e' } }] }

/** The dark OpenFreeMap style warmed up to the cabinet palette, with Russian labels. */
function warm(st: StyleSpecification): StyleSpecification {
  const s = structuredClone(st)
  for (const l of s.layers) {
    if (l.id === 'background' && l.type === 'background') l.paint = { ...l.paint, 'background-color': '#14110e' }
    if (l.id === 'water' && l.type === 'fill') l.paint = { ...l.paint, 'fill-color': '#0b0d10' }
    if (l.type === 'symbol' && l.layout?.['text-field'] && /name/.test(JSON.stringify(l.layout['text-field']))) {
      l.layout = { ...l.layout, 'text-field': ruName }
    }
  }
  return s
}

// world-atlas rings jump from +180 to -180 (Russia, Fiji…): fine for d3's spherical maths, but a flat GeoJSON
// renderer draws that jump as a band across the globe. Keep each ring continuous (longitudes may pass 180).
function unwrapRing(ring: number[][]): number[][] {
  let shift = 0
  return ring.map(([lon, lat], i) => {
    if (i) {
      const prev = ring[i - 1][0]
      if (lon - prev > 180) shift -= 360
      else if (prev - lon > 180) shift += 360
    }
    return [lon + shift, lat]
  })
}
function unwrap(g: Geometry): Geometry {
  if (g.type === 'Polygon') return { ...g, coordinates: g.coordinates.map(unwrapRing) }
  if (g.type === 'MultiPolygon') return { ...g, coordinates: g.coordinates.map((p) => p.map(unwrapRing)) }
  return g
}

function cssColor(el: Element, name: string, fallback: string) {
  const v = getComputedStyle(el).getPropertyValue(name).trim()
  return /^(#|rgb|hsl)/.test(v) ? v : fallback
}

// Fit the whole globe inside the shorter side, with room around its rim and the controls.
function fitZoom(el: HTMLElement) {
  const side = Math.min(el.clientWidth, el.clientHeight)
  return Math.max(0, Math.min(1.6, Math.log2(side / 225)))
}

function webglOK() {
  try {
    const c = document.createElement('canvas')
    return !!(c.getContext('webgl2') || c.getContext('webgl'))
  } catch {
    return false
  }
}

export default function GeoMap({ countries, points, compact }: { countries: Record<string, number>; points: GeoPoint[]; compact?: boolean }) {
  const [gl] = useState(webglOK)
  if (!gl) return <GeoSvg countries={countries} points={points} compact={compact} />
  return <GlMap countries={countries} points={points} compact={compact} />
}

function GlMap({ countries, points, compact }: { countries: Record<string, number>; points: GeoPoint[]; compact?: boolean }) {
  const box = useRef<HTMLDivElement>(null)
  const stage = useRef<HTMLDivElement>(null)
  const map = useRef<MapLibre | null>(null)
  const fitted = useRef(true)
  const [ready, setReady] = useState(false)
  const [failed, setFailed] = useState(false)
  const [mode, setMode] = useState<'globe' | 'map'>('globe')
  const [sel, setSel] = useState<GeoPoint | null>(null)
  const [width, setWidth] = useState(0)

  useEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setWidth(Math.max(1, Math.round(el.clientWidth))))
    ro.observe(el)
    setWidth(Math.max(1, Math.round(el.clientWidth)))
    return () => ro.disconnect()
  }, [])

  const top = useMemo(() => points.reduce<GeoPoint | null>((b, p) => (!b || p.count > b.count ? p : b), null), [points])
  const home = () => {
    fitted.current = true
    if (stage.current) map.current?.flyTo({ center: top ? [top.lon, top.lat] : [60, 50], zoom: fitZoom(stage.current), speed: 1.6 })
    setSel(null)
  }

  const pointsGeo = useMemo<FeatureCollection<Point>>(() => {
    const max = Math.max(1, ...points.map((p) => p.count))
    return {
      type: 'FeatureCollection',
      features: points.map((p, i) => ({ type: 'Feature', id: i, geometry: { type: 'Point', coordinates: [p.lon, p.lat] }, properties: { i, s: Math.sqrt(p.count / max) } })),
    }
  }, [points])

  // The map itself: created once, data is pushed into its sources below.
  useEffect(() => {
    const el = stage.current
    if (!el) return
    let live = true
    let m: MapLibre | null = null
    loadStyle().then((remote) => {
      if (!live) return
      try {
        m = new MapLibre({
          container: el,
          style: remote ? warm(remote) : offlineStyle,
          center: top ? [top.lon, top.lat] : [60, 50],
          zoom: fitZoom(el),
          maxZoom: MAX_ZOOM,
          attributionControl: { compact: true },
          dragRotate: false,
          pitchWithRotate: false,
        })
      } catch {
        setFailed(true)
        return
      }
      m.touchZoomRotate.disableRotation()
      m.on('zoomstart', (e) => {
        if (e.originalEvent) fitted.current = false
      })
      m.on('dragstart', () => {
        fitted.current = false
      })
      map.current = m
      if (import.meta.env.DEV) (window as unknown as { __geomap?: MapLibre }).__geomap = m // for browser checks
      const accent = cssColor(el, '--accent', '#f5a524')
      m.on('style.load', () => {
        if (!m) return
        m.setProjection({ type: 'globe' })
        const before = m.getLayer('waterway') ? 'waterway' : undefined
        m.addSource('countries', { type: 'geojson', data: { type: 'FeatureCollection', features: [] } })
        m.addLayer(
          {
            id: 'countries-fill',
            type: 'fill',
            source: 'countries',
            // shading fades out as streets appear
            paint: { 'fill-color': accent, 'fill-opacity': ['interpolate', ['linear'], ['zoom'], 0, ['*', 0.38, ['get', 't']], 4, ['*', 0.24, ['get', 't']], 6, 0] },
          },
          before,
        )
        m.addSource('points', { type: 'geojson', data: { type: 'FeatureCollection', features: [] } })
        const r = (lo: number, hi: number): ExpressionSpecification => ['interpolate', ['linear'], ['zoom'], 1, ['+', lo, ['*', 4, ['get', 's']]], 10, ['+', hi, ['*', 10, ['get', 's']]]]
        m.addLayer({ id: 'points-halo', type: 'circle', source: 'points', paint: { 'circle-radius': r(4, 10), 'circle-color': accent, 'circle-opacity': 0.28, 'circle-blur': 0.6 } })
        m.addLayer({
          id: 'points-core',
          type: 'circle',
          source: 'points',
          paint: { 'circle-radius': r(1.8, 4), 'circle-color': '#ffe8c4', 'circle-stroke-color': accent, 'circle-stroke-width': 1 },
        })
        setReady(true)
      })
      // A failed tile is not fatal; a failed style before the first load is — fall back to the offline globe.
      m.on('error', (e) => {
        if (!m || m.isStyleLoaded()) return
        if (remote && String(e.error?.message || '').includes('style')) m.setStyle(offlineStyle)
      })
      for (const id of ['points-core', 'points-halo']) {
        m.on('mouseenter', id, () => m && (m.getCanvas().style.cursor = 'pointer'))
        m.on('mouseleave', id, () => m && (m.getCanvas().style.cursor = ''))
      }
    })
    let lastWidth = el.clientWidth
    let lastHeight = el.clientHeight
    const ro = new ResizeObserver(() => {
      const width = el.clientWidth
      const height = el.clientHeight
      if (width === lastWidth && height === lastHeight) return
      lastWidth = width
      lastHeight = height
      const current = map.current
      if (!current || !width || !height) return
      current.resize()
      if (fitted.current) {
        current.stop()
        current.setZoom(fitZoom(el))
      }
    })
    ro.observe(el)
    return () => {
      live = false
      ro.disconnect()
      m?.remove()
      map.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Country shading.
  useEffect(() => {
    if (!ready) return
    let live = true
    loadAtlas().then((features) => {
      const src = map.current?.getSource('countries') as GeoJSONSource | undefined
      if (!live || !src) return
      const max = Math.max(1, ...Object.values(countries))
      src.setData({
        type: 'FeatureCollection',
        features: features
          .filter((f) => f.iso && countries[f.iso])
          .map((f) => ({ type: 'Feature', geometry: unwrap(f.geometry), properties: { t: Math.log1p(countries[f.iso!]) / Math.log1p(max) } })),
      })
    })
    return () => {
      live = false
    }
  }, [ready, countries])

  // Points, and a tap selects the nearest one.
  useEffect(() => {
    const m = map.current
    if (!ready || !m) return
    ;(m.getSource('points') as GeoJSONSource | undefined)?.setData(pointsGeo)
    setSel(null)
    const onClick = (e: { point: { x: number; y: number } }) => {
      const pad = 12 // finger-sized target around small dots
      const hits = m.queryRenderedFeatures(
        [
          [e.point.x - pad, e.point.y - pad],
          [e.point.x + pad, e.point.y + pad],
        ],
        { layers: ['points-core', 'points-halo'] },
      )
      const i = hits[0]?.properties?.i
      setSel(typeof i === 'number' ? points[i] : null)
    }
    m.on('click', onClick)
    return () => {
      m.off('click', onClick)
    }
  }, [ready, pointsGeo, points])

  // Fly to the busiest place whenever the data set changes.
  const topKey = top ? `${top.lat},${top.lon}` : ''
  useEffect(() => {
    if (ready && top) map.current?.jumpTo({ center: [top.lon, top.lat] })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, topKey])

  const pickMode = (v: 'globe' | 'map') => {
    setMode(v)
    map.current?.setProjection({ type: v === 'globe' ? 'globe' : 'mercator' })
  }

  if (failed) return <GeoSvg countries={countries} points={points} compact={compact} />

  return (
    <div className="geomap" ref={box}>
      <div className="geomap-bar">
        <div className="seg seg-fill seg-2 geomap-mode" role="tablist">
          <button className={mode === 'globe' ? 'on' : ''} onClick={() => pickMode('globe')}>
            Глобус
          </button>
          <button className={mode === 'map' ? 'on' : ''} onClick={() => pickMode('map')}>
            Карта
          </button>
        </div>
      </div>
      <div className="geomap-stage gl" style={{ height: compact ? Math.min(width || 300, 300) : `min(${Math.min(width || 460, 460)}px, 62vh)` }}>
        <div ref={stage} className="geomap-gl" role="img" aria-label="Карта запросов подписки: приближайте до улиц пальцами или колесом" />
        {!ready && <div className="geomap-loading small muted">Загружаем карту…</div>}
        <div className="geomap-zoom">
          <button onClick={() => {
            fitted.current = false
            map.current?.zoomIn()
          }} aria-label="Приблизить">
            +
          </button>
          <button onClick={() => {
            fitted.current = false
            map.current?.zoomOut()
          }} aria-label="Отдалить">
            −
          </button>
          <button onClick={home} aria-label="Вернуть вид" title="Вернуть вид">
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
          <button
            className="link-btn"
            onClick={() => {
              fitted.current = false
              map.current?.flyTo({ center: [sel.lon, sel.lat], zoom: Math.max(map.current.getZoom(), 11), speed: 1.4 })
            }}
          >
            приблизить
          </button>
        </div>
      )}
    </div>
  )
}
