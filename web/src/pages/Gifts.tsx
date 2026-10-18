import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { get, post, type Gift as GiftItem } from '../lib/api'
import { useApp } from '../lib/app'
import { date, plural } from '../lib/format'
import { haptic, openLink } from '../lib/tg'
import { Copy, Gift, Ticket, User } from '../ui/icons'
import { ErrorBox, PageLoader, Sheet, copy, useAction, useLoad, useToast } from '../ui/kit'

const giftLink = (code: string) => `${location.origin}/gifts?code=${encodeURIComponent(code)}`

/** Мои подарки (reference 3570): buy a gift, activate a code, list of bought gifts with code and link. */
export default function Gifts() {
  const { me, refreshMe } = useApp()
  const nav = useNavigate()
  const toast = useToast()
  const [params] = useSearchParams()
  const [code, setCode] = useState((params.get('code') || '').toUpperCase())
  const list = useLoad(() => get<GiftItem[]>('/api/gifts'))
  const { busy, run } = useAction()

  const redeem = () =>
    run(async () => {
      const r = await post<{ subscription_id: number; title: string }>('/api/gifts/redeem', { code })
      haptic('success')
      toast(`Подарок активирован: ${r.title}`)
      setCode('')
      await refreshMe()
      nav('/', { replace: true })
    })

  if (list.loading && !list.data) return <PageLoader />
  if (list.error) return <ErrorBox text={list.error} retry={list.reload} />
  const gifts = list.data || []

  return (
    <div className="stack lg">
      <h1>Мои подарки</h1>

      {me?.gifts_enabled && (
        <div className="card stack gift-hero">
          <div className="row" style={{ alignItems: 'flex-start', gap: 14 }}>
            <span className="action-ico accent">
              <Gift />
            </span>
            <div className="grow" style={{ display: 'grid', gap: 4 }}>
              <b>Подарить подписку</b>
              <span className="small muted">Оплатите тариф в подарок — получите код и ссылку, чтобы передать другу.</span>
            </div>
          </div>
          <button className="btn block" onClick={() => nav('/buy?gift=1')}>
            Выбрать тариф
          </button>
        </div>
      )}

      <form
        className="input-row"
        onSubmit={(e) => {
          e.preventDefault()
          redeem()
        }}
      >
        <input
          className="input"
          placeholder="GIFT-XXXX-XXXX"
          value={code}
          onChange={(e) => setCode(e.target.value.toUpperCase())}
          autoCapitalize="characters"
          autoComplete="off"
          spellCheck={false}
          aria-label="Код подарка"
        />
        <button className="btn secondary" disabled={busy || code.trim().length < 6}>
          Активировать
        </button>
      </form>

      {gifts.length === 0 ? (
        <div className="card empty-state">
          <Ticket />
          <p className="small">
            Здесь появятся купленные подарки.
            <br />
            Подарите подписку — код и ссылку можно будет переслать.
          </p>
        </div>
      ) : (
        <div className="stack">
          {gifts.map((g) => (
            <GiftCard key={g.id} g={g} />
          ))}
        </div>
      )}
    </div>
  )
}

function GiftCard({ g }: { g: GiftItem }) {
  const toast = useToast()
  const ready = g.status === 'ready'
  const share = () => openLink(`https://t.me/share/url?url=${encodeURIComponent(giftLink(g.code))}&text=${encodeURIComponent(`Дарю подписку ${g.title}! Код: ${g.code}`)}`)
  return (
    <div className={ready ? 'card stack gift-card' : 'card stack gift-card used'}>
      <div className="spread">
        <b>{g.title}</b>
        <span className={ready ? 'badge accent' : 'badge'}>{ready ? 'Не активирован' : 'Активирован'}</span>
      </div>
      <div className="small muted">
        {g.days} {plural(g.days, 'день', 'дня', 'дней')} · куплен {date(g.created_at)}
        {g.redeemed_at && ` · активирован ${date(g.redeemed_at)}`}
      </div>
      {ready && (
        <>
          <button className="gift-code num" onClick={async () => (await copy(g.code)) && toast('Код скопирован')} aria-label="Скопировать код">
            {g.code}
            <Copy />
          </button>
          <div className="row" style={{ gap: 8 }}>
            <button className="btn secondary small grow" onClick={async () => (await copy(giftLink(g.code))) && toast('Ссылка скопирована')}>
              <Copy /> Ссылка
            </button>
            <button className="btn small grow" onClick={share}>
              Отправить
            </button>
          </div>
        </>
      )}
    </div>
  )
}

/** Купить другому (reference: 3-step dialog). Step 1 finds the recipient here; steps 2–3 are the buy page. */
export function BuyForSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const nav = useNavigate()
  const [q, setQ] = useState('')
  const [found, setFound] = useState<{ name: string; q: string } | null>(null)
  const { busy, run } = useAction()

  const check = () =>
    run(async () => {
      const r = await post<{ name: string }>('/api/buy-for/lookup', { query: q.trim() })
      setFound({ name: r.name, q: q.trim() })
    })
  const close = () => {
    setFound(null)
    setQ('')
    onClose()
  }

  return (
    <Sheet open={open} onClose={close} title="Купить другому" icon={<User />}>
      <div className="stack">
        <div className="steps">
          <span className="tiny muted">ШАГ 1 ИЗ 3</span>
          <b>Кому покупаем</b>
          <div className="steps-bar" aria-hidden="true">
            <i className="on" />
            <i />
            <i />
          </div>
        </div>
        <p className="small muted">
          Оплатите подписку за друга или коллегу — деньги спишутся у вас, подписка появится у него. Укажите его Telegram ID, @username или email —
          точное совпадение.
        </p>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (found && found.q === q.trim()) {
              close()
              nav(`/buy?to=${encodeURIComponent(found.q)}&name=${encodeURIComponent(found.name)}`)
            } else check()
          }}
          className="stack"
        >
          <div className="input-ico">
            <User />
            <input
              className="input"
              placeholder="Telegram ID, @username или email"
              value={q}
              onChange={(e) => {
                setQ(e.target.value)
                setFound(null)
              }}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              inputMode="email"
              enterKeyHint="next"
            />
          </div>
          {found && (
            <div className="found">
              Получатель: <b>{found.name}</b>
            </div>
          )}
          <button className="btn block" disabled={busy || !q.trim()}>
            {found ? 'Выбрать тариф' : 'Продолжить'}
          </button>
        </form>
      </div>
    </Sheet>
  )
}
