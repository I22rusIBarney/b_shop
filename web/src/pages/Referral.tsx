import DOMPurify from 'dompurify'
import { useNavigate } from 'react-router-dom'
import { get, type ReferralInfo } from '../lib/api'
import { date, plural } from '../lib/format'
import { haptic, inTelegram, openLink } from '../lib/tg'
import { Back, Copy, Gift, People, Users } from '../ui/icons'
import { ErrorBox, PageLoader, copy, useLoad, useToast } from '../ui/kit'

const d = (n: number) => `${n} ${plural(n, 'день', 'дня', 'дней')}`

const kindLabel: Record<string, string> = {
  first: 'Друг оформил подписку',
  recurring: 'Друг продлил подписку',
  invitee: 'Бонус за приглашение',
  manual: 'Начислено администратором',
}

export default function Referral() {
  const nav = useNavigate()
  const toast = useToast()
  const info = useLoad(() => get<ReferralInfo>('/api/referral'))
  if (info.loading && !info.data) return <PageLoader />
  if (info.error) return <ErrorBox text={info.error} retry={info.reload} />
  const r = info.data!
  // In Telegram share the bot link; in a browser the cabinet link works for any login method.
  const link = inTelegram() ? r.link || r.web_link : r.web_link || r.link

  const doCopy = async () => {
    if (await copy(link)) {
      haptic('success')
      toast('Ссылка скопирована')
    }
  }
  const share = () => {
    const text = 'Быстрый и стабильный VPN — подключайся по моей ссылке'
    if (inTelegram()) openLink(`https://t.me/share/url?url=${encodeURIComponent(link)}&text=${encodeURIComponent(text)}`)
    else if (navigator.share) navigator.share({ url: link, text }).catch(() => {})
    else doCopy()
  }

  const progress = r.next_tier ? Math.min(100, (r.stats.paid / r.next_tier.min_invited) * 100) : 100

  return (
    <div className="stack lg">
      <div className="row">
        <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
          <Back />
        </button>
        <h1>Пригласить друзей</h1>
      </div>

      {!r.enabled ? (
        <div className="card empty-state">
          <People />
          <b style={{ color: 'var(--text)' }}>Программа приглашений на паузе</b>
          <p className="small">Когда она заработает, здесь появится ваша ссылка и бонусы за друзей.</p>
        </div>
      ) : (
        <>
          <div className="glass panel ref-hero">
            <Gift className="ref-gift" />
            {r.current_days > 0 && (
              <div className="ref-big">
                +{d(r.current_days)}
                <span>вам за каждого друга, который оформит подписку</span>
              </div>
            )}
            <ul className="ref-rules small">
              {r.invitee_days > 0 && <li>Другу — +{d(r.invitee_days)} к первой подписке</li>}
              {r.recurring_percent > 0 && <li>Вам — {r.recurring_percent}% дней от каждого продления друга</li>}
            </ul>
            {r.description && <div className="small muted guide-text" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(r.description) }} />}
          </div>

          {link && (
            <div className="glass panel stack">
              <div className="link-row" style={{ margin: 0 }}>
                <span className="link-ico">
                  <Users />
                </span>
                <div className="grow" style={{ minWidth: 0 }}>
                  <div className="link-label">Ваша ссылка</div>
                  <div className="link-url">
                    <span className="u">{link.replace('https://', '')}</span>
                  </div>
                </div>
                <button className="link-copy" onClick={doCopy} aria-label="Скопировать ссылку">
                  <Copy />
                </button>
              </div>
              <button className="pill-btn filled" onClick={share}>
                Поделиться ссылкой
              </button>
            </div>
          )}

          <div className="tiles">
            <div className="glass tile">
              <b className="num">{r.stats.invited}</b>
              <span>приглашено</span>
            </div>
            <div className="glass tile">
              <b className="num">{r.stats.paid}</b>
              <span>оплатили</span>
            </div>
            <div className="glass tile">
              <b className="num">{r.stats.days}</b>
              <span>{plural(r.stats.days, 'день', 'дня', 'дней')} получено</span>
            </div>
          </div>

          {r.tiers.length > 0 && (
            <div className="glass panel stack">
              <h3>Уровни</h3>
              {r.next_tier && (
                <div className="stack" style={{ gap: 6 }}>
                  <div className="small">
                    Ещё {r.next_tier.min_invited - r.stats.paid} {plural(r.next_tier.min_invited - r.stats.paid, 'оплативший друг', 'оплативших друга', 'оплативших друзей')} — и бонус станет +{d(r.next_tier.days)}
                  </div>
                  <div className="meter">
                    <i style={{ width: `${progress}%` }} />
                  </div>
                </div>
              )}
              <div className="list">
                {r.tiers.map((t, i) => {
                  const reached = r.stats.paid >= t.min_invited
                  return (
                    <div className="item" key={i} style={{ borderTop: i ? '1px solid var(--line)' : 0 }}>
                      <span className={reached ? 'badge accent' : 'badge'}>{reached ? '✓' : i + 1}</span>
                      <span className="grow">от {t.min_invited} {plural(t.min_invited, 'друга', 'друзей', 'друзей')}</span>
                      <b>+{d(t.days)}</b>
                    </div>
                  )
                })}
              </div>
            </div>
          )}

          {r.bonus_days > 0 && (
            <div className="glass panel small">
              🎁 На балансе <b>{d(r.bonus_days)}</b> — добавятся к вашей первой подписке.
            </div>
          )}

          <div className="glass panel stack">
            <h3>История начислений</h3>
            {r.bonuses.length === 0 ? (
              <p className="small muted">Пока пусто — поделитесь ссылкой с друзьями.</p>
            ) : (
              <div className="list">
                {r.bonuses.map((b, i) => (
                  <div className="item" key={b.id} style={{ borderTop: i ? '1px solid var(--line)' : 0 }}>
                    <div className="grow">
                      <div>{kindLabel[b.kind]}</div>
                      <div className="tiny muted">
                        {b.kind !== 'manual' && b.kind !== 'invitee' ? `${b.referral_name} · ` : ''}
                        {date(b.created_at)}
                      </div>
                    </div>
                    <b className="ok-text">+{d(b.days)}</b>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  )
}
