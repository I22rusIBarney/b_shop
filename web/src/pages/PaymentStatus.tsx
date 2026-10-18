import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { get } from '../lib/api'
import { useApp } from '../lib/app'
import { rub } from '../lib/format'
import { haptic, openLink } from '../lib/tg'
import { Check, Close } from '../ui/icons'
import { Spinner } from '../ui/kit'

interface Status {
  id: string
  status: 'pending' | 'succeeded' | 'canceled' | 'refunded'
  applied: boolean
  amount: number
  title: string
  confirmation_url: string
  operation: string
}

export default function PaymentStatus() {
  const { id } = useParams()
  const { me, config } = useApp()
  const [st, setSt] = useState<Status | null>(null)
  const [tries, setTries] = useState(0)

  useEffect(() => {
    if (!me || !id) return
    let stop = false
    let timer: number
    const tick = async (n: number) => {
      try {
        const s = await get<Status>(`/api/payments/${id}`)
        if (stop) return
        setSt(s)
        setTries(n)
        if ((s.status === 'succeeded' && s.applied) || s.status === 'canceled' || s.status === 'refunded') {
          if (s.status === 'succeeded') haptic('success')
          return
        }
      } catch {
        /* keep polling */
      }
      timer = window.setTimeout(() => tick(n + 1), n < 20 ? 3000 : 10000)
    }
    tick(0)
    return () => {
      stop = true
      window.clearTimeout(timer)
    }
  }, [id, me])

  // Opened in an external browser after paying, without a session.
  if (!me)
    return (
      <div className="card stack lg center">
        <h1>Оплата обрабатывается</h1>
        <p className="muted">Как только платёж пройдёт, подписка появится в кабинете, а бот пришлёт сообщение. Можно вернуться в Telegram.</p>
        {config.bot_username && (
          <a className="btn block" href={`https://t.me/${config.bot_username}`}>
            Вернуться в Telegram
          </a>
        )}
      </div>
    )

  const done = st?.status === 'succeeded' && st.applied
  const failed = st?.status === 'canceled' || st?.status === 'refunded'
  return (
    <div className="card stack lg center" aria-live="polite">
      {done ? (
        <>
          <div style={{ display: 'grid', placeItems: 'center' }}>
            <span style={{ display: 'grid', placeItems: 'center', width: 64, height: 64, borderRadius: '50%', background: 'var(--accent)', color: 'var(--accent-ink)' }}>
              <Check style={{ width: 32, height: 32 }} />
            </span>
          </div>
          <h1>Оплата прошла</h1>
          <p className="muted">
            {st!.title} · {rub(st!.amount)}.{' '}
            {st!.operation === 'gift'
              ? 'Подарок готов — код и ссылка в разделе «Мои подарки».'
              : st!.operation === 'buy_for'
                ? 'Подписка выдана получателю, мы сообщили ему об этом.'
                : 'Подписка уже активна.'}
          </p>
          <Link className="btn block" to={st!.operation === 'gift' ? '/gifts' : '/'}>
            {st!.operation === 'gift' ? 'Мои подарки' : 'В кабинет'}
          </Link>
        </>
      ) : failed ? (
        <>
          <div style={{ display: 'grid', placeItems: 'center' }}>
            <span style={{ display: 'grid', placeItems: 'center', width: 64, height: 64, borderRadius: '50%', background: 'var(--surface-2)', color: 'var(--danger)' }}>
              <Close style={{ width: 30, height: 30 }} />
            </span>
          </div>
          <h1>Платёж не прошёл</h1>
          <p className="muted">Деньги не списаны. Попробуйте ещё раз или выберите другой способ оплаты.</p>
          <Link className="btn block" to="/buy">
            Выбрать заново
          </Link>
        </>
      ) : (
        <>
          <div style={{ display: 'grid', placeItems: 'center' }}>
            <Spinner large />
          </div>
          <h1>{st?.status === 'succeeded' ? 'Выдаём подписку' : 'Ждём оплату'}</h1>
          <p className="muted">
            {st?.status === 'succeeded'
              ? 'Платёж получен, подключаем подписку. Обычно это занимает несколько секунд.'
              : 'Завершите оплату на странице банка. Эта страница обновится сама.'}
          </p>
          {st?.status === 'pending' && st.confirmation_url && (
            <button className="btn secondary block" onClick={() => openLink(st.confirmation_url)}>
              Открыть страницу оплаты
            </button>
          )}
          {tries > 30 && (
            <p className="small muted">
              Если деньги списались, а подписка не появилась — напишите в <Link to="/support">поддержку</Link>, мы всё проверим.
            </p>
          )}
        </>
      )}
    </div>
  )
}
