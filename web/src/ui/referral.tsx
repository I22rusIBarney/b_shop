import { Link } from 'react-router-dom'
import type { ReferralInfo } from '../lib/api'
import { plural } from '../lib/format'
import { haptic, inTelegram, openLink } from '../lib/tg'
import { Chevron, Copy, People, Send } from './icons'
import { copy, useToast } from './kit'

export const days = (n: number) => `${n} ${plural(n, 'день', 'дня', 'дней')}`

/** The link to hand out here: the bot link inside Telegram, the cabinet link in a browser. */
export function mainLink(r: ReferralInfo) {
  return inTelegram() ? r.link || r.web_link : r.web_link || r.link
}

/** Copy and share actions for a referral link (Telegram share sheet, system share, or copy as a fallback). */
export function useShare() {
  const toast = useToast()
  const doCopy = async (link: string, what = 'Ссылка скопирована') => {
    if (await copy(link)) {
      haptic('success')
      toast(what)
    }
  }
  const share = (link: string) => {
    const text = 'Быстрый и стабильный VPN — подключайся по моей ссылке'
    if (inTelegram()) openLink(`https://t.me/share/url?url=${encodeURIComponent(link)}&text=${encodeURIComponent(text)}`)
    else if (navigator.share) navigator.share({ url: link, text }).catch(() => {})
    else doCopy(link)
  }
  return { copy: doCopy, share }
}

/** One line of what both sides get, e.g. "Вам и другу — по +3 дня за подписку". */
export function benefitLine(r: ReferralInfo) {
  const me = r.current_days
  if (me > 0 && r.invitee_days === me) return `Вам и другу — по +${days(me)} за подписку`
  if (me > 0 && r.invitee_days > 0) return `Вам +${days(me)}, другу +${days(r.invitee_days)} за подписку`
  if (me > 0) return `+${days(me)} за каждого друга с подпиской`
  if (r.invitee_days > 0) return `Другу +${days(r.invitee_days)} к первой подписке`
  if (r.recurring_percent > 0) return `${r.recurring_percent}% дней с каждой оплаты друга`
  return 'Делитесь ссылкой с друзьями'
}

/** Progress to the next tier when tiers exist, else the share of friends with a running subscription. */
export function progressOf(r: ReferralInfo): { pct: number; text: string } | null {
  if (r.next_tier) {
    const left = r.next_tier.min_invited - r.stats.paid
    return {
      pct: Math.min(100, (r.stats.paid / r.next_tier.min_invited) * 100),
      text: `Ещё ${left} ${plural(left, 'оплативший друг', 'оплативших друга', 'оплативших друзей')} — и бонус станет +${days(r.next_tier.days)}`,
    }
  }
  if (r.stats.invited === 0) return null
  return {
    pct: (r.stats.active / r.stats.invited) * 100,
    text: `С подпиской ${r.stats.active} из ${r.stats.invited} ${plural(r.stats.invited, 'приглашённого', 'приглашённых', 'приглашённых')}`,
  }
}

/** Profile card: what you earned, one line of what both sides get, and the two actions. */
export function ReferralCard({ r }: { r: ReferralInfo }) {
  const { copy: doCopy, share } = useShare()
  const link = mainLink(r)
  const earned = r.stats.days > 0
  const big = earned ? r.stats.days : r.current_days
  const p = progressOf(r)
  return (
    <section className="ref-card" aria-label="Приглашайте друзей">
      <Link to="/referral" className="ref-card-head">
        <span className="ref-card-ico" aria-hidden="true">
          <People />
        </span>
        <span className="grow">
          <b>Приглашайте друзей</b>
          <span>{benefitLine(r)}</span>
        </span>
        <Chevron className="chev" />
      </Link>
      {big > 0 && (
        <Link to="/referral" className="ref-card-sum">
          <b className="num">+{days(big)}</b>
          <span>{earned ? 'вы уже получили за друзей' : 'за каждого друга, который оформит подписку'}</span>
        </Link>
      )}
      {p && (
        <div className="ref-card-progress">
          <div className="meter">
            <i style={{ width: `${p.pct}%` }} />
          </div>
          <span>{p.text}</span>
        </div>
      )}
      {link && (
        <div className="ref-card-actions">
          <button type="button" className="pill-btn filled" onClick={() => share(link)}>
            <Send />
            Пригласить
          </button>
          {r.web_link && (
            <button type="button" className="pill-btn" onClick={() => doCopy(r.web_link, 'Веб-ссылка скопирована')} aria-label="Скопировать веб-ссылку">
              <Copy />
              Веб-ссылка
            </button>
          )}
        </div>
      )}
    </section>
  )
}
