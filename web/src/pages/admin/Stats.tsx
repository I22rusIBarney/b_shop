import { useEffect, useState, type ReactNode } from 'react'
import { NavLink, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { get, post } from '../../lib/api'
import { relative, rubRound, shortDate } from '../../lib/format'
import { HBars, Kpi, TimeChart, bucketLabel, type Point } from '../../ui/charts'
import { ErrorBox, Spinner, useAction, useLoad, pressable } from '../../ui/kit'
import { GeoSettings, GeoView, type GeoReport } from './Geo'

interface Named {
  key: string
  label: string
  count: number
  sum: number
}
interface Delta {
  cur: number
  prev: number
}
interface Range {
  Group: string
  Period: string
}

const tabs = [
  ['overview', 'Обзор'],
  ['economics', 'Юнит-экономика'],
  ['payments', 'Платежи'],
  ['referrals', 'Рефералы'],
  ['support', 'Поддержка'],
  ['analytics', 'Аналитика'],
  ['geo', 'География'],
] as const

const periods = [
  ['7d', '7 дней'],
  ['1m', 'Месяц'],
  ['3m', '3 месяца'],
  ['6m', '6 месяцев'],
  ['12m', 'Год'],
  ['all', 'Всё время'],
]
const groups = [
  ['', 'Авто'],
  ['day', 'Дни'],
  ['week', 'Недели'],
  ['month', 'Месяцы'],
]

const n = (v: number) => v.toLocaleString('ru-RU')
const pctOf = (a: number, b: number) => (b ? `${Math.round((a / b) * 1000) / 10}%` : '—')
const minutes = (m: number) => (m <= 0 ? '—' : m < 60 ? `${Math.round(m)} мин` : m < 60 * 48 ? `${Math.round((m / 60) * 10) / 10} ч` : `${Math.round(m / 60 / 24)} дн`)

function Card({ title, wide, children, action }: { title: ReactNode; wide?: boolean; children: ReactNode; action?: ReactNode }) {
  return (
    <section className={wide ? 'card wide' : 'card'}>
      <div className="card-title">
        <h3 style={{ margin: 0 }}>{title}</h3>
        {action}
      </div>
      {children}
    </section>
  )
}

function usePeriod(def = '1m') {
  const [params, setParams] = useSearchParams()
  const period = params.get('period') || def
  const group = params.get('group') || ''
  const set = (k: string, v: string) => {
    const p = new URLSearchParams(params)
    if (v) p.set(k, v)
    else p.delete(k)
    setParams(p, { replace: true })
  }
  const bar = (
    <div className="stats-bar">
      <div className="chips">
        {periods.map(([k, l]) => (
          <button key={k} className={period === k ? 'chip on' : 'chip'} onClick={() => set('period', k)}>
            {l}
          </button>
        ))}
      </div>
      <select className="input" style={{ width: 'auto', minHeight: 38 }} value={group} onChange={(e) => set('group', e.target.value)} aria-label="Группировка">
        {groups.map(([k, l]) => (
          <option key={k} value={k}>
            {l}
          </option>
        ))}
      </select>
    </div>
  )
  return { period, group, qs: `period=${period}&group=${group}`, bar, set }
}

function Loaded<T>({ load, children }: { load: { data?: T | null; error?: string | null; reload: () => void }; children: (d: T) => ReactNode }) {
  if (load.error) return <ErrorBox text={load.error} retry={load.reload} />
  if (!load.data)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner large />
      </div>
    )
  return <>{children(load.data)}</>
}

export default function Stats() {
  const { tab = 'overview' } = useParams()
  return (
    <>
      <div className="admin-head">
        <h1>Статистика</h1>
      </div>
      <div className="chips" role="tablist">
        {tabs.map(([k, l]) => (
          <NavLink key={k} to={`/admin/stats/${k}`} className={({ isActive }) => (isActive ? 'chip on' : 'chip')}>
            {l}
          </NavLink>
        ))}
      </div>
      {tab === 'overview' && <Overview />}
      {tab === 'economics' && <Economics />}
      {tab === 'payments' && <PaymentsTab />}
      {tab === 'referrals' && <Referrals />}
      {tab === 'support' && <Support />}
      {tab === 'analytics' && <Analytics />}
      {tab === 'geo' && <GeoTab />}
    </>
  )
}

// ---------------------------------------------------------------------------------------------

interface Overview {
  range: Range
  segments: { key: string; label: string; all: number; bot_blocked: number; blocked: number }[]
  users: { Total: number; DAU: number; MAU: number; NewToday: number; NewWeek: number }
  plans: Named[]
  subs_total: number
  subs_expired: number
  subs_unlimited: number
  revenue: { Today: Delta; Week: Delta; Month: Delta; AvgCheck: number; AllTime: number; Expiring7: number; Expiring30: number; Expiring30Sum: number }
  new_users: Point[]
  income: Point[]
  methods: Named[]
  referrals: { Referred: number; ActiveReferrers: number; BonusDays: number; Paid: number }
  promos: { Total: number; Active: number; Uses: number; BonusDays: number }
  by_plan: Named[]
  by_months: Named[]
  purchases: Point[]
  autopay: { Enabled: number; SavedMethods: number }
  trial: { Used: number; Paid: number }
  extras: Named[]
  logins: { Sessions: number; Today: number; Week: number; Month: number; by_provider: Named[] }
  languages: Named[]
}

function Overview() {
  const p = usePeriod()
  const load = useLoad(() => get<Overview>(`/api/admin/stats/overview?${p.qs}`), [p.qs])
  const nav = useNavigate()
  return (
    <>
      {p.bar}
      <Loaded load={load}>
        {(o) => {
          const g = o.range.Group
          const segTotal = o.segments.reduce((a, s) => a + s.all, 0)
          return (
            <>
              <div className="kpis">
                <Kpi label="Доход сегодня" value={rubRound(o.revenue.Today.cur)} delta={o.revenue.Today} hint="к вчера" />
                <Kpi label="За 7 дней" value={rubRound(o.revenue.Week.cur)} delta={o.revenue.Week} hint="к прошлым 7 дням" />
                <Kpi label="С начала месяца" value={rubRound(o.revenue.Month.cur)} delta={o.revenue.Month} hint="к тому же дню прошлого" />
                <Kpi label="Средний чек" value={rubRound(o.revenue.AvgCheck)} hint={`всего ${rubRound(o.revenue.AllTime)}`} />
                <Kpi label="Пользователей" value={n(o.users.Total)} hint={`+${n(o.users.NewToday)} сегодня · +${n(o.users.NewWeek)} за неделю`} />
                <Kpi label="DAU / MAU" value={`${n(o.users.DAU)} / ${n(o.users.MAU)}`} hint={pctOf(o.users.DAU, o.users.MAU)} />
                <Kpi label="Истекают без автопродления" value={`${n(o.revenue.Expiring7)} / ${n(o.revenue.Expiring30)}`} hint={`за 7 / 30 дней · под риском ${rubRound(o.revenue.Expiring30Sum)}`} />
              </div>
              <div className="stats-grid">
                <Card title="Доходы" wide>
                  <TimeChart points={o.income.map((x) => ({ t: x.t, v: [x.v[0]] }))} series={[{ name: 'Доход' }]} group={g} format={rubRound} money />
                </Card>
                <Card title="Новые пользователи" wide>
                  <TimeChart points={o.new_users} series={[{ name: 'Новые' }]} group={g} />
                </Card>
                <Card title="Пользователи по подписке" wide>
                  <div className="table-wrap">
                    <table className="t">
                      <thead>
                        <tr>
                          <th>Сегмент</th>
                          <th>Всего</th>
                          <th title="Заблокировали бота в Telegram">Блок бота</th>
                          <th title="Заблокированы администратором">Блок</th>
                        </tr>
                      </thead>
                      <tbody>
                        {o.segments.map((s) => (
                          <tr key={s.key}>
                            <td>{s.label}</td>
                            <td className="num">
                              {n(s.all)} <span className="muted tiny">{pctOf(s.all, segTotal)}</span>
                            </td>
                            <td className="num">{n(s.bot_blocked)}</td>
                            <td className="num">{n(s.blocked)}</td>
                          </tr>
                        ))}
                        <tr>
                          <td>
                            <b>Итого</b>
                          </td>
                          <td className="num">
                            <b>{n(segTotal)}</b>
                          </td>
                          <td className="num">{n(o.segments.reduce((a, s) => a + s.bot_blocked, 0))}</td>
                          <td className="num">{n(o.segments.reduce((a, s) => a + s.blocked, 0))}</td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                </Card>
                <Card title="Активные подписки по тарифам">
                  <HBars items={o.plans.map((x) => ({ key: x.key, label: x.label, value: x.count, note: pctOf(x.count, o.plans.reduce((a, y) => a + y.count, 0)) }))} />
                  <p className="muted tiny" style={{ marginTop: 12 }}>
                    Всего подписок {n(o.subs_total)} · истекли {n(o.subs_expired)} · без срока {n(o.subs_unlimited)}
                  </p>
                </Card>
                <Card title="Покупки по тарифам">
                  <HBars items={o.by_plan.map((x) => ({ key: x.key, label: x.label, value: x.sum, note: `${n(x.count)} шт.` }))} format={rubRound} />
                </Card>
                <Card title="Покупки по сроку">
                  <HBars items={o.by_months.map((x) => ({ key: x.key, label: x.label, value: x.count, note: rubRound(x.sum) }))} />
                </Card>
                <Card title="Способы оплаты">
                  <HBars items={o.methods.map((x) => ({ key: x.key, label: x.label, value: x.sum, note: `${n(x.count)} шт.` }))} format={rubRound} />
                </Card>
                <Card title="Покупки по типам операций" wide>
                  <TimeChart
                    points={o.purchases}
                    series={[{ name: 'Покупка' }, { name: 'Продление' }, { name: 'Смена тарифа' }, { name: 'Автопродление' }]}
                    group={g}
                  />
                </Card>
                <Card title="Пробный период">
                  <div className="mini-list">
                    <span>Брали пробный</span>
                    <span>{n(o.trial.Used)}</span>
                    <span>Потом оплатили</span>
                    <span>{n(o.trial.Paid)}</span>
                    <span>Конверсия</span>
                    <span>{pctOf(o.trial.Paid, o.trial.Used)}</span>
                  </div>
                </Card>
                <Card title="Реферальная программа" action={<button className="link-btn tiny" onClick={() => nav('/admin/stats/referrals')}>подробнее</button>}>
                  <div className="mini-list">
                    <span>Приглашено</span>
                    <span>{n(o.referrals.Referred)}</span>
                    <span>Из них оплатили</span>
                    <span>{n(o.referrals.Paid)}</span>
                    <span>Активных пригласивших</span>
                    <span>{n(o.referrals.ActiveReferrers)}</span>
                    <span>Выдано бонусных дней</span>
                    <span>{n(o.referrals.BonusDays)}</span>
                    <span>Конверсия</span>
                    <span>{pctOf(o.referrals.Paid, o.referrals.Referred)}</span>
                  </div>
                </Card>
                <Card title="Промокоды">
                  <div className="mini-list">
                    <span>Общих кодов</span>
                    <span>{n(o.promos.Total)}</span>
                    <span>Действуют</span>
                    <span>{n(o.promos.Active)}</span>
                    <span>Использований</span>
                    <span>{n(o.promos.Uses)}</span>
                    <span>Бонусных дней</span>
                    <span>{n(o.promos.BonusDays)}</span>
                  </div>
                </Card>
                <Card title="Автопродление и докупки">
                  <div className="mini-list">
                    <span>Подписок с автопродлением</span>
                    <span>{n(o.autopay.Enabled)}</span>
                    <span>Клиентов с сохранённой картой</span>
                    <span>{n(o.autopay.SavedMethods)}</span>
                    {o.extras.map((x) => (
                      <FragmentPair key={x.key} a={`${x.label}: покупок`} b={`${n(x.count)} · ${rubRound(x.sum)}`} />
                    ))}
                  </div>
                </Card>
                <Card title="Входы">
                  <div className="mini-list" style={{ marginBottom: 14 }}>
                    <span>Активных сессий</span>
                    <span>{n(o.logins.Sessions)}</span>
                    <span>Уникальных сегодня / неделя / месяц</span>
                    <span>
                      {n(o.logins.Today)} / {n(o.logins.Week)} / {n(o.logins.Month)}
                    </span>
                  </div>
                  <HBars items={o.logins.by_provider.map((x) => ({ key: x.key, label: x.label, value: x.count, note: `${n(x.sum)} чел.` }))} />
                </Card>
                <Card title="Языки">
                  <HBars items={o.languages.map((x) => ({ key: x.key, label: x.label.toUpperCase(), value: x.count }))} />
                </Card>
              </div>
            </>
          )
        }}
      </Loaded>
    </>
  )
}

function FragmentPair({ a, b }: { a: ReactNode; b: ReactNode }) {
  return (
    <>
      <span>{a}</span>
      <span>{b}</span>
    </>
  )
}

// ---------------------------------------------------------------------------------------------

interface MonthE {
  month: string
  mrr: number
  new: number
  reactivated: number
  expansion: number
  contraction: number
  churn: number
  active: number
  churned: number
  new_count: number
  arppu: number
  revenue: number
}
interface Economics {
  months: MonthE[]
  cards: { MRR: number; MRRPrev: number; ARR: number; Active: number; ARPPU: number; LTV: number; Churn: number; Lifetime: number; NRR: number; QuickRatio: number }
  cohorts: { month: string; size: number; retention: number[]; rev_per_user: number }[]
  retention: { days_to_churn: number; reactivation_share: number }
  ltv: { Paying: number; Users: number; DaysToFirst: number; DaysMedian: number; by_source: Named[]; by_plan: Named[] }
}

function Economics() {
  const [months, setMonths] = useState(6)
  const load = useLoad(() => get<Economics>(`/api/admin/stats/economics?months=${months}`), [months])
  const r1 = (x: number) => Math.round(x * 10) / 10
  return (
    <>
      <div className="stats-bar">
        <div className="chips">
          {[3, 6, 12].map((m) => (
            <button key={m} className={months === m ? 'chip on' : 'chip'} onClick={() => setMonths(m)}>
              {m} мес.
            </button>
          ))}
        </div>
        <span className="muted tiny">MRR считается из оплат: сумма делится на оплаченные месяцы и учитывается, пока подписка оплачена</span>
      </div>
      <Loaded load={load}>
        {(e) => {
          const c = e.cards
          const pts = (f: (m: MonthE) => number[]) => e.months.slice(1).map((m) => ({ t: `${m.month}-01`, v: f(m) }))
          return (
            <>
              <div className="kpis">
                <Kpi label="MRR" value={rubRound(c.MRR)} delta={{ cur: c.MRR, prev: c.MRRPrev }} hint="к прошлому месяцу" />
                <Kpi label="ARR" value={rubRound(c.ARR)} />
                <Kpi label="Платящих сейчас" value={n(c.Active)} />
                <Kpi label="Отток в месяц" value={`${r1(c.Churn)}%`} />
                <Kpi label="ARPPU" value={rubRound(c.ARPPU)} hint="в месяц" />
                <Kpi label="Срок жизни" value={c.Lifetime ? `${r1(c.Lifetime)} мес.` : '—'} />
                <Kpi label="LTV" value={c.LTV ? rubRound(c.LTV) : '—'} hint="ARPPU × срок жизни" />
                <Kpi label="NRR" value={c.NRR ? `${r1(c.NRR)}%` : '—'} hint="удержание выручки" />
                <Kpi label="Quick Ratio" value={c.QuickRatio ? r1(c.QuickRatio) : '—'} hint="приток / потери" />
              </div>
              <div className="stats-grid">
                <Card title="MRR по месяцам" wide>
                  <TimeChart points={pts((m) => [m.mrr])} series={[{ name: 'MRR' }]} group="month" format={rubRound} money />
                </Card>
                <Card title="Прирост MRR" wide>
                  <TimeChart
                    points={pts((m) => [m.new, m.reactivated, m.expansion])}
                    series={[{ name: 'Новые' }, { name: 'Вернувшиеся' }, { name: 'Расширение' }]}
                    group="month"
                    format={rubRound}
                    money
                  />
                </Card>
                <Card title="Потери MRR" wide>
                  <TimeChart points={pts((m) => [-m.churn, -m.contraction])} series={[{ name: 'Ушли' }, { name: 'Понизили тариф' }]} group="month" format={rubRound} money />
                </Card>
                <Card title="Детализация по месяцам" wide>
                  <div className="table-wrap">
                    <table className="t">
                      <thead>
                        <tr>
                          <th>Месяц</th>
                          <th>MRR</th>
                          <th>Новый</th>
                          <th>Вернувшиеся</th>
                          <th>Расширение</th>
                          <th>Понижение</th>
                          <th>Отток</th>
                          <th>Net</th>
                          <th>Платящих</th>
                          <th>Ушли</th>
                          <th>ARPPU</th>
                          <th>Собрано</th>
                        </tr>
                      </thead>
                      <tbody>
                        {[...e.months.slice(1)].reverse().map((m) => {
                          const net = m.new + m.reactivated + m.expansion + m.contraction + m.churn
                          return (
                            <tr key={m.month}>
                              <td>{bucketLabel(`${m.month}-01`, 'month', true)}</td>
                              <td className="num">{rubRound(m.mrr)}</td>
                              <td className="num">{rubRound(m.new)}</td>
                              <td className="num">{rubRound(m.reactivated)}</td>
                              <td className="num">{rubRound(m.expansion)}</td>
                              <td className="num">{rubRound(m.contraction)}</td>
                              <td className="num">{rubRound(m.churn)}</td>
                              <td className={net >= 0 ? 'num delta up' : 'num delta down'}>{rubRound(net)}</td>
                              <td className="num">{n(m.active)}</td>
                              <td className="num">{n(m.churned)}</td>
                              <td className="num">{rubRound(m.arppu)}</td>
                              <td className="num">{rubRound(m.revenue)}</td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                </Card>
                <Card title="Платящие и ушедшие" wide>
                  <TimeChart points={pts((m) => [m.active, m.churned])} series={[{ name: 'Платящие' }, { name: 'Ушли' }]} group="month" kind="line" />
                </Card>
                <Card title="Когорты: доля платящих по месяцам" wide>
                  <div className="table-wrap">
                    <table className="t cohort">
                      <thead>
                        <tr>
                          <th>Первая оплата</th>
                          <th>Клиентов</th>
                          {[0, 1, 2, 3, 4, 5].map((k) => (
                            <th key={k}>M{k}</th>
                          ))}
                          <th>Доход на клиента</th>
                        </tr>
                      </thead>
                      <tbody>
                        {[...e.cohorts].reverse().map((co) => (
                          <tr key={co.month}>
                            <td>{bucketLabel(`${co.month}-01`, 'month', true)}</td>
                            <td className="num">{n(co.size)}</td>
                            {co.retention.map((v, k) => (
                              <td
                                key={k}
                                className="cell"
                                style={v >= 0 && co.size ? { background: `color-mix(in srgb, #3987e5 ${Math.round(v * 0.7)}%, transparent)` } : undefined}
                              >
                                {v < 0 || !co.size ? '' : `${v}%`}
                              </td>
                            ))}
                            <td className="num">{co.size ? rubRound(co.rev_per_user) : '—'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </Card>
                <Card title="Удержание">
                  <div className="mini-list">
                    <span>В среднем платят до ухода</span>
                    <span>{e.retention.days_to_churn ? `${n(e.retention.days_to_churn)} дн.` : '—'}</span>
                    <span>Ушедших вернулось</span>
                    <span>{e.retention.reactivation_share}%</span>
                  </div>
                </Card>
                <Card title="LTV">
                  <div className="mini-list" style={{ marginBottom: 14 }}>
                    <span>Платящих из всех</span>
                    <span>
                      {n(e.ltv.Paying)} из {n(e.ltv.Users)} · {pctOf(e.ltv.Paying, e.ltv.Users)}
                    </span>
                    <span>До первой оплаты, дней</span>
                    <span>
                      {e.ltv.DaysToFirst} (медиана {e.ltv.DaysMedian})
                    </span>
                  </div>
                  <HBars items={e.ltv.by_source.map((x) => ({ key: x.key, label: x.label, value: x.sum, note: `${n(x.count)} чел.` }))} format={rubRound} />
                </Card>
                <Card title="LTV по первому тарифу">
                  <HBars items={e.ltv.by_plan.map((x) => ({ key: x.key, label: x.label, value: x.sum, note: `${n(x.count)} чел.` }))} format={rubRound} />
                </Card>
              </div>
            </>
          )
        }}
      </Loaded>
    </>
  )
}

// ---------------------------------------------------------------------------------------------

interface PayStats {
  range: Range
  attempts: number
  succeeded: number
  failed: number
  pending: number
  refunded: number
  retry_success: number
  autopay_share: number
  conversion: Point[]
  methods: Named[]
  methods_ok: Named[]
  operations: Named[]
}

function PaymentsTab() {
  const p = usePeriod()
  const load = useLoad(() => get<PayStats>(`/api/admin/stats/payments?${p.qs}`), [p.qs])
  return (
    <>
      {p.bar}
      <Loaded load={load}>
        {(s) => {
          const ok = Object.fromEntries(s.methods_ok.map((m) => [m.key, m.count]))
          return (
            <>
              <div className="kpis">
                <Kpi label="Попыток оплаты" value={n(s.attempts)} />
                <Kpi label="Успешных" value={n(s.succeeded)} hint={`конверсия ${pctOf(s.succeeded + s.refunded, s.attempts)}`} />
                <Kpi label="Неудачных" value={n(s.failed)} hint={s.pending ? `ещё ждут ${n(s.pending)}` : undefined} />
                <Kpi label="Возвратов" value={n(s.refunded)} />
                <Kpi label="Оплатили после неудачи" value={`${s.retry_success}%`} hint="в течение суток" />
                <Kpi label="Доля автоплатежей" value={`${s.autopay_share}%`} />
              </div>
              <div className="stats-grid">
                <Card title="Оплаты и отказы" wide>
                  <TimeChart points={s.conversion} series={[{ name: 'Успешные' }, { name: 'Неудачные' }]} group={s.range.Group} />
                </Card>
                <Card title="Способы оплаты: конверсия">
                  <HBars
                    items={s.methods.map((m) => ({ key: m.key, label: m.label, value: m.count, note: `успешно ${pctOf(ok[m.key] || 0, m.count)}` }))}
                  />
                </Card>
                <Card title="Способы оплаты: выручка">
                  <HBars items={s.methods.map((m) => ({ key: m.key, label: m.label, value: m.sum }))} format={rubRound} />
                </Card>
                <Card title="По операциям">
                  <HBars items={s.operations.map((m) => ({ key: m.key, label: m.label, value: m.sum, note: `${n(m.count)} шт.` }))} format={rubRound} />
                </Card>
              </div>
            </>
          )
        }}
      </Loaded>
    </>
  )
}

// ---------------------------------------------------------------------------------------------

interface RefStats {
  range: Range
  invited: number
  paid: number
  bonus_days: number
  revenue: number
  bonus_cost: number
  day_price: number
  roi: number
  series: Point[]
  bonuses: Point[]
  by_kind: Named[]
  funnel: { Invited: number; Started: number; Paid: number; Repeat: number }
  ltv_invited: number
  ltv_regular: number
  top: { id: number; name: string; invited: number; paid: number; bonus_days: number; revenue: number }[] | null
  recent: { at: string; kind: string; days: number; recipient: string; recipient_id: number; referral: string }[] | null
}

function GeoTab() {
  const p = usePeriod('1m')
  const load = useLoad(() => get<GeoReport>(`/api/admin/stats/geo?period=${p.period}`), [p.period])
  // While the geolocation database is still being downloaded, check again every few seconds.
  const waiting = load.data && load.data.status.enabled && !load.data.status.ready && !load.data.status.db_error
  useEffect(() => {
    if (!waiting) return
    const t = setInterval(() => load.refresh(), 8000)
    return () => clearInterval(t)
  }, [waiting, load.refresh])
  return (
    <>
      <div className="stats-bar">
        <div className="chips">
          {periods.map(([k, l]) => (
            <button key={k} className={p.period === k ? 'chip on' : 'chip'} onClick={() => p.set('period', k)}>
              {l}
            </button>
          ))}
        </div>
      </div>
      <Loaded load={load}>
        {(r) => (
          <div className="stack lg">
            <GeoView report={r} />
            <GeoSettings onSaved={load.reload} />
          </div>
        )}
      </Loaded>
    </>
  )
}

function Referrals() {
  const p = usePeriod('3m')
  const load = useLoad(() => get<RefStats>(`/api/admin/stats/referrals?${p.qs}`), [p.qs])
  const nav = useNavigate()
  return (
    <>
      {p.bar}
      <Loaded load={load}>
        {(s) => (
          <>
            <div className="kpis">
              <Kpi label="Приглашено" value={n(s.invited)} />
              <Kpi label="Оплатили" value={n(s.paid)} hint="приглашённых с оплатой за период" />
              <Kpi label="Выдано дней" value={n(s.bonus_days)} />
              <Kpi label="Выручка от приглашённых" value={rubRound(s.revenue)} />
              <Kpi label="Стоимость бонусов" value={rubRound(s.bonus_cost)} hint={`день ≈ ${rubRound(s.day_price)}`} />
              <Kpi label="ROI" value={s.bonus_cost ? `${s.roi}%` : '—'} />
            </div>
            <div className="stats-grid">
              <Card title="Приглашённые и их оплаты" wide>
                <TimeChart points={s.series} series={[{ name: 'Новые приглашённые' }, { name: 'Оплаты приглашённых' }]} group={s.range.Group} kind="line" />
              </Card>
              <Card title="Бонусные дни по типам" wide>
                <TimeChart
                  points={s.bonuses}
                  series={[{ name: 'За первую оплату' }, { name: 'За повторную' }, { name: 'Приглашённому' }, { name: 'Вручную' }]}
                  group={s.range.Group}
                />
              </Card>
              <Card title="Воронка приглашённых">
                <HBars
                  max={s.funnel.Invited || 1}
                  items={[
                    { key: 'i', label: 'Пришли по ссылке', value: s.funnel.Invited },
                    { key: 's', label: 'Начали оформление', value: s.funnel.Started, note: pctOf(s.funnel.Started, s.funnel.Invited) },
                    { key: 'p', label: 'Оплатили', value: s.funnel.Paid, note: pctOf(s.funnel.Paid, s.funnel.Invited) },
                    { key: 'r', label: 'Оплатили повторно', value: s.funnel.Repeat, note: pctOf(s.funnel.Repeat, s.funnel.Invited) },
                  ]}
                />
              </Card>
              <Card title="LTV: приглашённый и обычный">
                <HBars
                  items={[
                    { key: 'inv', label: 'Приглашённый', value: s.ltv_invited },
                    { key: 'reg', label: 'Обычный', value: s.ltv_regular },
                  ]}
                  format={rubRound}
                />
              </Card>
              <Card title="Типы бонусов">
                <HBars items={s.by_kind.map((x) => ({ key: x.key, label: x.label, value: x.sum, note: `${n(x.count)} раз` }))} format={(v) => `${n(v)} дн.`} />
              </Card>
              <Card title="Лучшие пригласившие" wide>
                {!s.top?.length ? (
                  <p className="muted small">Пока никто никого не пригласил</p>
                ) : (
                  <div className="table-wrap">
                    <table className="t">
                      <thead>
                        <tr>
                          <th>Клиент</th>
                          <th>Пригласил</th>
                          <th>Оплатили</th>
                          <th>Получил дней</th>
                          <th>Принесли</th>
                        </tr>
                      </thead>
                      <tbody>
                        {s.top.map((t) => (
                          <tr key={t.id} className="click" {...pressable(() => nav(`/admin/customers/${t.id}`))}>
                            <td>{t.name}</td>
                            <td className="num">{n(t.invited)}</td>
                            <td className="num">{n(t.paid)}</td>
                            <td className="num">{n(t.bonus_days)}</td>
                            <td className="num">{rubRound(t.revenue)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </Card>
              <Card title="Последние бонусы" wide>
                {!s.recent?.length ? (
                  <p className="muted small">Бонусов ещё не было</p>
                ) : (
                  <div className="table-wrap">
                    <table className="t">
                      <thead>
                        <tr>
                          <th>Когда</th>
                          <th>Тип</th>
                          <th>Дней</th>
                          <th>Получил</th>
                          <th>За кого</th>
                        </tr>
                      </thead>
                      <tbody>
                        {s.recent.map((r, i) => (
                          <tr key={i} className="click" {...pressable(() => nav(`/admin/customers/${r.recipient_id}`))}>
                            <td>{relative(r.at)}</td>
                            <td>{r.kind}</td>
                            <td className="num">{n(r.days)}</td>
                            <td>{r.recipient}</td>
                            <td>{r.referral || '—'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </Card>
            </div>
          </>
        )}
      </Loaded>
    </>
  )
}

// ---------------------------------------------------------------------------------------------

interface SupStats {
  range: Range
  total: number
  this_month: Delta
  per_user: number
  per_day: number
  first_avg: number
  first_median: number
  resolve_avg: number
  resolve_median: number
  rating: number
  rated: number
  ai_share: number
  statuses: Named[]
  series: Point[]
}

function Support() {
  const p = usePeriod()
  const load = useLoad(() => get<SupStats>(`/api/admin/stats/support?${p.qs}`), [p.qs])
  return (
    <>
      {p.bar}
      <Loaded load={load}>
        {(s) => (
          <>
            <div className="kpis">
              <Kpi label="Обращений за период" value={n(s.total)} hint={`${s.per_day} в день · ${s.per_user} на клиента`} />
              <Kpi label="В этом месяце" value={n(s.this_month.cur)} delta={s.this_month} invert hint="к тому же дню прошлого" />
              <Kpi label="Первый ответ" value={minutes(s.first_avg)} hint={`медиана ${minutes(s.first_median)}`} />
              <Kpi label="Решение" value={minutes(s.resolve_avg)} hint={`медиана ${minutes(s.resolve_median)}`} />
              <Kpi label="Оценка" value={s.rated ? `${s.rating} ★` : '—'} hint={s.rated ? `${n(s.rated)} оценок` : undefined} />
              <Kpi label="Отвечал ИИ" value={`${s.ai_share}%`} hint="обращений" />
            </div>
            <div className="stats-grid">
              <Card title="Обращения" wide>
                <TimeChart points={s.series.map((x) => ({ t: x.t, v: [x.v[0]] }))} series={[{ name: 'Обращений' }]} group={s.range.Group} />
              </Card>
              <Card title="Среднее время первого ответа" wide>
                <TimeChart points={s.series.map((x) => ({ t: x.t, v: [x.v[1]] }))} series={[{ name: 'Первый ответ' }]} group={s.range.Group} format={minutes} />
              </Card>
              <Card title="По статусам">
                <HBars items={s.statuses.map((x) => ({ key: x.key, label: x.label, value: x.count, note: pctOf(x.count, s.total) }))} />
              </Card>
            </div>
          </>
        )}
      </Loaded>
    </>
  )
}

// ---------------------------------------------------------------------------------------------

interface Step {
  key: string
  label: string
  cur: number
  prev: number
}
interface AnStats {
  range: Range
  funnel: Step[]
  steps: Point[]
  events: Step[]
  sources: { key: string; label: string; users: number; paid: number; conversion: number }[] | null
  systems: { key: string; label: string; attempts: number; paid: number; share: number; conversion: number }[] | null
  leads: { key: string; label: string; count: number; converted: number }[]
}
interface Lead {
  id: number
  name: string
  temperature: string
  score: number
  last_seen: string | null
  created_at: string
  plan: string
  signals: string[] | null
}

const tempBadge: Record<string, string> = { hot: 'badge bad', warm: 'badge warn', cool: 'badge accent', cold: 'badge', stable: 'badge ok' }
const tempName: Record<string, string> = { hot: 'горячий', warm: 'тёплый', cool: 'прохладный', cold: 'холодный', stable: 'стабильный' }

function Analytics() {
  const p = usePeriod()
  const load = useLoad(() => get<AnStats>(`/api/admin/stats/analytics?${p.qs}`), [p.qs])
  const [temp, setTemp] = useState('')
  const [q, setQ] = useState('')
  const leads = useLoad(() => get<{ items: Lead[]; total: number }>(`/api/admin/stats/leads?temperature=${temp}&q=${encodeURIComponent(q)}&limit=50`), [temp, q])
  const { busy, run } = useAction()
  const nav = useNavigate()
  const recompute = () =>
    run(async () => {
      const r = await post<{ changed: number }>('/api/admin/stats/leads/recompute')
      load.reload()
      leads.reload()
      return `Пересчитано, изменилось: ${r.changed}`
    })
  const fmtDelta = (s: Step) => {
    if (p.period === 'all' || !s.prev) return null
    const d = Math.round(((s.cur - s.prev) / s.prev) * 100)
    return <span className={d >= 0 ? 'delta up' : 'delta down'}>{` ${d >= 0 ? '▲' : '▼'}${Math.abs(d)}%`}</span>
  }
  return (
    <>
      {p.bar}
      <Loaded load={load}>
        {(a) => {
          const top = a.funnel[0]?.cur || 0
          return (
            <div className="stats-grid">
              <Card title="Воронка продаж (по зарегистрированным за период)">
                <HBars
                  max={top || 1}
                  items={a.funnel.map((s, i) => ({
                    key: s.key,
                    label: s.label,
                    value: s.cur,
                    note: (
                      <>
                        {i > 0 ? pctOf(s.cur, top) : '100%'}
                        {fmtDelta(s)}
                      </>
                    ),
                  }))}
                />
              </Card>
              <Card title="События за период">
                <div className="mini-list">
                  {a.events.map((s) => (
                    <FragmentPair
                      key={s.key}
                      a={s.label}
                      b={
                        <>
                          {n(s.cur)}
                          {fmtDelta(s)}
                        </>
                      }
                    />
                  ))}
                </div>
              </Card>
              <Card title="Этапы воронки по дате регистрации" wide>
                <TimeChart
                  points={a.steps}
                  series={[{ name: 'Регистрация' }, { name: 'Пробный' }, { name: 'Счёт' }, { name: 'Оплата' }]}
                  group={a.range.Group}
                  kind="line"
                />
              </Card>
              <Card title="Источники">
                <HBars items={(a.sources || []).map((s) => ({ key: s.key, label: s.label, value: s.users, note: `оплатили ${s.conversion}%` }))} />
              </Card>
              <Card title="Платёжные системы">
                <HBars items={(a.systems || []).map((s) => ({ key: s.key, label: s.label, value: s.paid, note: `доля ${s.share}% · конверсия ${s.conversion}%` }))} />
              </Card>
              <Card
                title="Температура лидов"
                wide
                action={
                  <button className="btn secondary small" disabled={busy} onClick={recompute}>
                    Пересчитать
                  </button>
                }
              >
                <div className="kpis" style={{ marginBottom: 12 }}>
                  {a.leads.map((l) => (
                    <div key={l.key} className="kpi">
                      <span className="kpi-label">{l.label}</span>
                      <b className="num">{n(l.count)}</b>
                      <span className="kpi-sub">
                        {l.key !== 'stable' && <span className="muted">оплатили за 30 дн.: {pctOf(l.converted, l.count)}</span>}
                        <button className="link-btn tiny" disabled={!l.count} onClick={() => nav(`/admin/broadcasts?segment=lead_${l.key}`)}>
                          Рассылка
                        </button>
                      </span>
                    </div>
                  ))}
                </div>
                <div className="filters">
                  <input className="input grow" placeholder="Поиск: @username, почта, Telegram ID" value={q} onChange={(e) => setQ(e.target.value)} />
                  <select className="input" value={temp} onChange={(e) => setTemp(e.target.value)}>
                    <option value="">Все</option>
                    {Object.entries(tempName).map(([k, v]) => (
                      <option key={k} value={k}>
                        {v}
                      </option>
                    ))}
                  </select>
                </div>
                {leads.error ? (
                  <ErrorBox text={leads.error} retry={leads.reload} />
                ) : !leads.data ? (
                  <Spinner />
                ) : !leads.data.items.length ? (
                  <p className="muted small">Лидов нет. Температура считается, когда включены автоматизации, или по кнопке «Пересчитать».</p>
                ) : (
                  <div className="table-wrap">
                    <table className="t">
                      <thead>
                        <tr>
                          <th>Клиент</th>
                          <th>Скор</th>
                          <th>Статус</th>
                          <th>Сигналы</th>
                          <th>Тариф</th>
                          <th>Был</th>
                          <th>С нами с</th>
                        </tr>
                      </thead>
                      <tbody>
                        {leads.data.items.map((l) => (
                          <tr key={l.id} className="click" {...pressable(() => nav(`/admin/customers/${l.id}`))}>
                            <td>{l.name}</td>
                            <td className="num">
                              <b>{l.score}</b>
                            </td>
                            <td>
                              <span className={tempBadge[l.temperature] || 'badge'}>{tempName[l.temperature] || l.temperature}</span>
                            </td>
                            <td>
                              <div className="lead-tags">
                                {(l.signals || []).map((s) => (
                                  <span key={s} className="badge">
                                    {s}
                                  </span>
                                ))}
                              </div>
                            </td>
                            <td>{l.plan || '—'}</td>
                            <td style={{ whiteSpace: 'nowrap' }}>{l.last_seen ? relative(l.last_seen) : '—'}</td>
                            <td style={{ whiteSpace: 'nowrap' }}>{shortDate(l.created_at)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                {leads.data && leads.data.total > leads.data.items.length && (
                  <p className="muted tiny" style={{ marginTop: 8 }}>
                    Показаны 50 с самым высоким скором из {n(leads.data.total)}
                  </p>
                )}
              </Card>
            </div>
          )
        }}
      </Loaded>
    </>
  )
}

