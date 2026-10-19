import React, { useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { ChartIcon, TableIcon } from './icons'

// Small hand-built SVG charts for the admin statistics. One axis per chart, thin marks,
// recessive grid, hover tooltip on every mark, legend for ≥ 2 series, table view on demand.

// Categorical order validated for the dark surface (#151210): CVD ΔE ≥ 8.4, normal ΔE ≥ 19.8.
export const SERIES = ['#3987e5', '#d95926', '#199e70', '#c98500']

export interface Point {
  t: string
  v: number[]
}
export interface Series {
  name: string
  color?: string
}

const MONTHS = ['янв', 'фев', 'мар', 'апр', 'мая', 'июн', 'июл', 'авг', 'сен', 'окт', 'ноя', 'дек']

export function bucketLabel(t: string, group: string, long = false): string {
  const [y, m, d] = t.split('-').map(Number)
  if (group === 'month') return long ? `${MONTHS[m - 1]} ${y}` : `${MONTHS[m - 1]}${m === 1 ? ` ${String(y).slice(2)}` : ''}`
  const s = `${d} ${MONTHS[m - 1]}`
  if (!long) return s
  if (group === 'week') {
    const e = new Date(y, m - 1, d + 6)
    return `${s} – ${e.getDate()} ${MONTHS[e.getMonth()]}`
  }
  return s
}

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [w, setW] = useState(320)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    setW(Math.max(240, Math.floor(el.clientWidth)))
    const ro = new ResizeObserver(([e]) => setW(Math.max(240, Math.floor(e.contentRect.width))))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, w] as const
}

function niceMax(v: number, whole = false): number {
  // whole: counts of people/tickets — the middle grid line must stay an integer (no «0,5 клиента»)
  if (v <= 0) return whole ? 2 : 1
  const p = Math.pow(10, Math.floor(Math.log10(v)))
  for (const m of whole ? [1, 2, 5, 10] : [1, 2, 2.5, 5, 10]) if (m * p >= v) return whole ? Math.max(2, m * p) : m * p
  return 10 * p
}

const compact = (n: number) =>
  Math.abs(n) >= 1e6 ? `${(n / 1e6).toLocaleString('ru-RU', { maximumFractionDigits: 1 })} млн` : Math.abs(n) >= 1e4 ? `${Math.round(n / 1e3).toLocaleString('ru-RU')} тыс` : n.toLocaleString('ru-RU')

export function Legend({ series }: { series: Series[] }) {
  if (series.length < 2) return null
  return (
    <div className="legend">
      {series.map((s, i) => (
        <span key={s.name}>
          <i style={{ background: s.color || SERIES[i] }} />
          {s.name}
        </span>
      ))}
    </div>
  )
}

/** Bars over time; with several series they stack (stack) or the first is bars and the rest are lines (never a second axis). */
export function TimeChart({
  points,
  series,
  group,
  format = (n) => n.toLocaleString('ru-RU'),
  kind = 'stack',
  height = 200,
  money,
  summary,
}: {
  points: Point[]
  series: Series[]
  group: string
  format?: (n: number) => string
  /** stack: bars (flows per period); line: first series bars, the rest lines; area: one level over time (MRR, active users) */
  kind?: 'stack' | 'line' | 'area'
  height?: number
  /** values are kopecks: the axis shows rubles */
  money?: boolean
  /** headline above the plot: the period total (flows) or the latest value (levels) */
  summary?: 'sum' | 'last'
}) {
  const [ref, width] = useWidth<HTMLDivElement>()
  const [hover, setHover] = useState<number | null>(null)
  const [table, setTable] = useState(false)
  const left = money ? 64 : 44
  const right = 8
  const top = 8
  const bottom = 22
  const n = points.length || 1
  const plotW = width - left - right
  const plotH = height - top - bottom
  const totals = points.map((p) => (kind === 'stack' ? p.v.reduce((a, b) => a + Math.max(b, 0), 0) : Math.max(...p.v, 0)))
  const max = money ? niceMax(Math.max(0, ...totals) / 100) * 100 : niceMax(Math.max(0, ...totals), true)
  const y = (v: number) => top + plotH - (v / max) * plotH
  const step = plotW / n
  const barW = Math.max(2, Math.min(24, step - (step > 8 ? 4 : 1)))
  const labelEvery = Math.ceil(n / Math.max(2, Math.floor(plotW / 64)))
  const colors = series.map((s, i) => s.color || (series.length === 1 ? 'var(--accent)' : SERIES[i]))
  const empty = totals.every((t) => t === 0)
  const gid = useId()
  const cx = (i: number) => left + step * i + step / 2
  const head =
    summary === 'sum'
      ? { v: points.reduce((a, p) => a + (p.v[0] || 0), 0), cap: 'за период' }
      : summary === 'last' && points.length
        ? { v: points[points.length - 1].v[0] || 0, cap: 'сейчас' }
        : null

  return (
    <div className="chart" ref={ref}>
      <div className="chart-head">
        {head ? (
          <div className="chart-total">
            <b className="num">{format(head.v)}</b>
            <span>{head.cap}</span>
          </div>
        ) : (
          <Legend series={series} />
        )}
        <button className="chart-view" onClick={() => setTable(!table)} aria-label={table ? 'Показать график' : 'Показать таблицей'} title={table ? 'График' : 'Таблица'}>
          {table ? <ChartIcon /> : <TableIcon />}
        </button>
      </div>
      {head && <Legend series={series} />}
      {table ? (
        <DataTable points={points} series={series} group={group} format={format} />
      ) : (
        <div className="chart-plot" onMouseLeave={() => setHover(null)}>
          <svg width={width} height={height} role="img" aria-label={series.map((s) => s.name).join(', ')}>
            {[0, 0.5, 1].map((f) => (
              <g key={f}>
                <line x1={left} x2={width - right} y1={y(max * f)} y2={y(max * f)} className="grid" />
                <text x={left - 6} y={y(max * f) + 4} textAnchor="end" className="axis">
                  {money ? `${compact(Math.round((max * f) / 100))} ₽` : compact(max * f)}
                </text>
              </g>
            ))}
            {points.map((p, i) => {
              const cx = left + step * i + step / 2
              let acc = 0
              return (
                <g key={p.t}>
                  {(kind === 'area' ? [] : kind === 'stack' ? p.v : p.v.slice(0, 1)).map((v, k) => {
                    if (v <= 0) return null
                    const y0 = y(acc)
                    acc += v
                    const y1 = y(acc)
                    const isTop = kind !== 'stack' || p.v.slice(k + 1).every((x) => x <= 0)
                    const h = Math.max(1, y0 - y1 - (k > 0 ? 2 : 0))
                    const r = isTop ? Math.min(4, barW / 2, h) : 0
                    const x = cx - barW / 2
                    const yy = y1
                    return (
                      <path
                        key={k}
                        fill={colors[k]}
                        opacity={hover === null || hover === i ? 1 : 0.45}
                        d={`M${x},${yy + h} V${yy + r} Q${x},${yy} ${x + r},${yy} H${x + barW - r} Q${x + barW},${yy} ${x + barW},${yy + r} V${yy + h} Z`}
                      />
                    )
                  })}
                  {i % labelEvery === 0 && (
                    <text x={cx} y={height - 6} textAnchor="middle" className="axis">
                      {bucketLabel(p.t, group)}
                    </text>
                  )}
                </g>
              )
            })}
            {kind === 'line' &&
              series.slice(1).map((s, k) => {
                const d = points.map((p, i) => `${i ? 'L' : 'M'}${left + step * i + step / 2},${y(p.v[k + 1] || 0)}`).join(' ')
                return (
                  <g key={s.name}>
                    <path d={d} fill="none" stroke={colors[k + 1]} strokeWidth={2} strokeLinejoin="round" />
                    {hover !== null && points[hover] && (
                      <circle cx={left + step * hover + step / 2} cy={y(points[hover].v[k + 1] || 0)} r={4} fill={colors[k + 1]} stroke="var(--surface-solid)" strokeWidth={2} />
                    )}
                  </g>
                )
              })}
            {kind === 'area' && points.length > 0 && (() => {
              const xy = points.map((p, i) => [cx(i), y(Math.max(0, p.v[0] || 0))] as const)
              const line = smooth(xy)
              const base = top + plotH
              const last = xy[xy.length - 1]
              const at = hover !== null ? xy[hover] : null
              return (
                <g>
                  <defs>
                    <linearGradient id={gid} x1="0" x2="0" y1="0" y2="1">
                      <stop offset="0" stopColor={colors[0]} stopOpacity={0.22} />
                      <stop offset="1" stopColor={colors[0]} stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <path d={`${line} L${last[0]},${base} L${xy[0][0]},${base} Z`} fill={`url(#${CSS.escape(gid)})`} className="area-fill" />
                  <path d={line} fill="none" stroke={colors[0]} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" className="area-line" />
                  {(at || last) && <circle cx={(at || last)[0]} cy={(at || last)[1]} r={4.5} fill={colors[0]} stroke="var(--surface-solid)" strokeWidth={2} />}
                </g>
              )
            })()}
            {hover !== null && <line x1={left + step * hover + step / 2} x2={left + step * hover + step / 2} y1={top} y2={top + plotH} className="crosshair" />}
            {points.map((p, i) => (
              <rect key={p.t} x={left + step * i} y={top} width={step} height={plotH + bottom} fill="transparent" onMouseEnter={() => setHover(i)} onClick={() => setHover(i)} />
            ))}
            {empty && (
              <text x={left + plotW / 2} y={top + plotH / 2} textAnchor="middle" className="axis">
                нет данных за период
              </text>
            )}
          </svg>
          {hover !== null && points[hover] && (
            <div className="tip" style={{ left: Math.min(width - 170, Math.max(0, left + step * hover + step / 2 - 80)) }}>
              <b>{bucketLabel(points[hover].t, group, true)}</b>
              {series.map((s, k) => (
                <div key={s.name}>
                  <i style={{ background: colors[k] }} />
                  <span>{s.name}</span>
                  <b className="num">{format(points[hover].v[k] || 0)}</b>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// Monotone cubic through the points (no overshoot below zero or above the peak), as an SVG path.
function smooth(pts: readonly (readonly [number, number])[]): string {
  if (pts.length < 2) return pts.length ? `M${pts[0][0]},${pts[0][1]}` : ''
  const n = pts.length
  const d: number[] = []
  const m: number[] = []
  for (let i = 0; i < n - 1; i++) d.push((pts[i + 1][1] - pts[i][1]) / (pts[i + 1][0] - pts[i][0]))
  m.push(d[0])
  for (let i = 1; i < n - 1; i++) m.push(d[i - 1] * d[i] <= 0 ? 0 : (2 * d[i - 1] * d[i]) / (d[i - 1] + d[i]))
  m.push(d[n - 2])
  let s = `M${pts[0][0]},${pts[0][1]}`
  for (let i = 0; i < n - 1; i++) {
    const [x0, y0] = pts[i]
    const [x1, y1] = pts[i + 1]
    const h = (x1 - x0) / 3
    s += ` C${x0 + h},${y0 + m[i] * h} ${x1 - h},${y1 - m[i + 1] * h} ${x1},${y1}`
  }
  return s
}

function DataTable({ points, series, group, format }: { points: Point[]; series: Series[]; group: string; format: (n: number) => string }) {
  return (
    <div className="table-wrap" style={{ maxHeight: 260, overflowY: 'auto' }}>
      <table className="t">
        <thead>
          <tr>
            <th>Период</th>
            {series.map((s) => (
              <th key={s.name}>{s.name}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {[...points].reverse().map((p) => (
            <tr key={p.t}>
              <td>{bucketLabel(p.t, group, true)}</td>
              {series.map((s, k) => (
                <td key={s.name} className="num">
                  {format(p.v[k] || 0)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** Horizontal bars for breakdowns (plans, methods, funnel). Value text stays in text ink. */
export function HBars({ items, format = (n) => n.toLocaleString('ru-RU'), extra, max: forcedMax }: {
  items: { key: string; label: ReactNode; value: number; note?: ReactNode }[]
  format?: (n: number) => string
  extra?: (key: string) => ReactNode
  max?: number
}) {
  const max = forcedMax ?? Math.max(1, ...items.map((i) => i.value))
  if (!items.length) return <p className="muted small">Нет данных за период</p>
  return (
    <div className="hbars">
      {items.map((it) => (
        <div key={it.key} className="hbar" title={`${typeof it.label === 'string' ? it.label : ''}: ${format(it.value)}`}>
          <div className="hbar-top">
            <span>{it.label}</span>
            <span className="num">
              {format(it.value)}
              {it.note && <span className="muted"> · {it.note}</span>}
              {extra?.(it.key)}
            </span>
          </div>
          <div className="hbar-track">
            <div style={{ width: `${Math.max(it.value > 0 ? 1.5 : 0, (it.value / max) * 100)}%` }} />
          </div>
        </div>
      ))}
    </div>
  )
}

/** KPI tiles in balanced rows: as many per row as fit, then spread evenly so the last row never holds a lone tile. */
export function Kpis({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const [cols, setCols] = useState(0)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const fit = () => {
      const n = el.children.length || 1
      const per = Math.max(1, Math.floor((el.clientWidth + 12) / (148 + 12)))
      const rows = Math.ceil(n / per)
      setCols(Math.ceil(n / rows))
    }
    const ro = new ResizeObserver(fit)
    ro.observe(el)
    fit()
    return () => ro.disconnect()
  }, [])
  return (
    <div className="kpis" ref={ref} style={cols ? ({ '--cols': cols } as React.CSSProperties) : undefined}>
      {children}
    </div>
  )
}

export function Kpi({ label, value, delta, hint, invert }: { label: ReactNode; value: ReactNode; delta?: { cur: number; prev: number }; hint?: ReactNode; invert?: boolean }) {
  let d: ReactNode = null
  if (delta) {
    if (delta.prev === 0) d = delta.cur > 0 ? <span className="delta muted">новое</span> : null
    else {
      const p = Math.round(((delta.cur - delta.prev) / delta.prev) * 100)
      const good = invert ? p <= 0 : p >= 0
      d = (
        <span className={good ? 'delta up' : 'delta down'}>
          {p >= 0 ? '▲' : '▼'} {Math.abs(p)}%
        </span>
      )
    }
  }
  return (
    <div className="kpi">
      <span className="kpi-label">{label}</span>
      <b className="num">{value}</b>
      {(d || hint) && (
        <span className="kpi-sub">
          {d}
          {hint && <span className="muted">{hint}</span>}
        </span>
      )}
    </div>
  )
}
