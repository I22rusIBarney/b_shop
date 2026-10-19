import DOMPurify from 'dompurify'
import { useNavigate } from 'react-router-dom'
import { get, type ReferralInfo } from '../lib/api'
import { date, plural } from '../lib/format'
import { Back, Copy, Gift, People, Send, Users } from '../ui/icons'
import { ErrorBox, PageLoader, useLoad } from '../ui/kit'
import { days as d, mainLink, progressOf, useShare } from '../ui/referral'

const kindLabel: Record<string, string> = {
  first: 'Друг оформил подписку',
  recurring: 'Друг продлил подписку',
  invitee: 'Бонус за приглашение',
  manual: 'Начислено администратором',
}

const friendStatus: Record<string, { label: string; cls: string }> = {
  active: { label: 'с подпиской', cls: 'badge ok' },
  paid: { label: 'подписка закончилась', cls: 'badge' },
  joined: { label: 'ещё не оформил', cls: 'badge' },
}

export default function Referral() {
  const nav = useNavigate()
  const { copy, share } = useShare()
  const info = useLoad(() => get<ReferralInfo>('/api/referral'))
  if (info.loading && !info.data) return <PageLoader />
  if (info.error) return <ErrorBox text={info.error} retry={info.reload} />
  const r = info.data!
  const link = mainLink(r)
  const p = progressOf(r)

  return (
    <div className="stack lg ref-page">
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
            {r.stats.days > 0 && (
              <div className="ref-earned">
                <b className="num">+{d(r.stats.days)}</b>
                <span>вы уже получили за друзей</span>
              </div>
            )}
            <div className="ref-sides">
              {r.current_days > 0 && (
                <div>
                  <span>Вам</span>
                  <b className="num">+{d(r.current_days)}</b>
                  <small>за каждого друга с подпиской</small>
                </div>
              )}
              {r.invitee_days > 0 && (
                <div>
                  <span>Другу</span>
                  <b className="num">+{d(r.invitee_days)}</b>
                  <small>к его первой подписке</small>
                </div>
              )}
            </div>
            {r.recurring_percent > 0 && (
              <p className="small" style={{ margin: 0 }}>
                И ещё {r.recurring_percent}% дней от каждого продления друга — пока он остаётся с нами.
              </p>
            )}
          </div>

          {link && (
            <div className="glass panel stack">
              <button className="pill-btn filled" onClick={() => share(link)}>
                <Send />
                Пригласить друга
              </button>
              {r.link && (
                <LinkRow label="Ссылка в Telegram" url={r.link} onCopy={() => copy(r.link, 'Ссылка для Telegram скопирована')} />
              )}
              {r.web_link && (
                <LinkRow label="Ссылка для браузера — если у друга нет Telegram" url={r.web_link} onCopy={() => copy(r.web_link, 'Ссылка для браузера скопирована')} />
              )}
            </div>
          )}

          <div className="glass panel stack">
            <h3>Как это работает</h3>
            <ol className="ref-steps">
              <li>
                <b>Отправьте ссылку</b>
                <span>Другу, в чат или в соцсети — любой из ссылок выше.</span>
              </li>
              <li>
                <b>Друг оформляет подписку</b>
                <span>Регистрация по ссылке закрепляет его за вами.</span>
              </li>
              <li>
                <b>Дни приходят сами</b>
                <span>Добавим к вашей подписке и пришлём сообщение. Без подписки — сохраним до первой.</span>
              </li>
            </ol>
          </div>

          <div className="ref-tiles">
            <div className="glass ref-tile">
              <b className="num">{r.stats.invited}</b>
              <span>{plural(r.stats.invited, 'приглашён', 'приглашено', 'приглашено')}</span>
            </div>
            <div className="glass ref-tile">
              <b className="num">{r.stats.active}</b>
              <span>с подпиской</span>
            </div>
            <div className="glass ref-tile">
              <b className="num">{r.stats.days}</b>
              <span>{plural(r.stats.days, 'день', 'дня', 'дней')} получено</span>
            </div>
          </div>

          {(p || r.tiers.length > 0) && (
            <div className="glass panel stack">
              <h3>{r.tiers.length > 0 ? 'Уровни' : 'Прогресс'}</h3>
              {p && (
                <div className="stack" style={{ gap: 6 }}>
                  <div className="small">{p.text}</div>
                  <div className="meter">
                    <i style={{ width: `${p.pct}%` }} />
                  </div>
                </div>
              )}
              {r.tiers.length > 0 && (
                <div className="list">
                  {r.tiers.map((t, i) => {
                    const reached = r.stats.paid >= t.min_invited
                    return (
                      <div className="item" key={i} style={{ borderTop: i ? '1px solid var(--line)' : 0 }}>
                        <span className={reached ? 'badge accent' : 'badge'}>{reached ? '✓' : i + 1}</span>
                        <span className="grow">
                          от {t.min_invited} {plural(t.min_invited, 'друга', 'друзей', 'друзей')}
                        </span>
                        <b>+{d(t.days)}</b>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {r.bonus_days > 0 && (
            <div className="glass panel small">
              <p style={{ margin: 0 }}>
                🎁 На балансе <b>{d(r.bonus_days)}</b> — добавятся к вашей первой подписке.
              </p>
            </div>
          )}

          <div className="glass panel stack">
            <h3>Ваши друзья</h3>
            {r.friends.length === 0 ? (
              <p className="small muted" style={{ margin: 0 }}>
                Пока никого. Отправьте ссылку одному другу — с этого всё начинается.
              </p>
            ) : (
              <div className="list">
                {r.friends.map((f, i) => (
                  <div className="item" key={i} style={{ borderTop: i ? '1px solid var(--line)' : 0 }}>
                    <span className="ref-ava" aria-hidden="true">
                      {f.name[0]}
                    </span>
                    <div className="grow" style={{ minWidth: 0 }}>
                      <div className="ref-name">{f.name}</div>
                      <span className={friendStatus[f.status].cls}>{friendStatus[f.status].label}</span>
                    </div>
                    {f.days > 0 && <b className="ok-text num">+{d(f.days)}</b>}
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="glass panel stack">
            <h3>История начислений</h3>
            {r.bonuses.length === 0 ? (
              <p className="small muted" style={{ margin: 0 }}>
                Здесь появятся дни за друзей.
              </p>
            ) : (
              <div className="list">
                {r.bonuses.map((b, i) => (
                  <div className="item" key={b.id} style={{ borderTop: i ? '1px solid var(--line)' : 0 }}>
                    <div className="grow">
                      <div>{kindLabel[b.kind]}</div>
                      <div className="tiny muted">
                        {b.kind !== 'manual' && b.kind !== 'invitee' ? `${b.referral_name} · ` : ''}
                        {date(b.created_at)}
                        {b.revoked_at ? ' · отменён: друг вернул оплату' : ''}
                      </div>
                    </div>
                    {b.revoked_at ? <s className="muted num">+{d(b.days)}</s> : <b className="ok-text num">+{d(b.days)}</b>}
                  </div>
                ))}
              </div>
            )}
          </div>

          <details className="glass panel ref-terms">
            <summary>Условия</summary>
            <ul className="small">
              <li>Бонус начисляется, когда друг оплачивает подписку: покупку, продление или автопродление. Пробный период, подарки, смена тарифа, докупки и бесплатные покупки (скидка 100%) не в счёт.</li>
              <li>Друг закрепляется за вами при первой регистрации по ссылке — позже пригласившего не поменять.</li>
              <li>Дни добавляются к вашей подписке. Если подписки нет — копятся и добавятся к первой.</li>
              <li>Если друг вернул оплату полностью, бонус за неё отменяется.</li>
            </ul>
            {r.description && <div className="small muted guide-text" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(r.description) }} />}
          </details>
        </>
      )}
    </div>
  )
}

function LinkRow({ label, url, onCopy }: { label: string; url: string; onCopy: () => void }) {
  return (
    <div className="link-row" style={{ margin: 0 }}>
      <span className="link-ico">
        <Users />
      </span>
      <div className="grow" style={{ minWidth: 0 }}>
        <div className="link-label">{label}</div>
        <div className="link-url">
          <span className="u">{url.replace('https://', '')}</span>
        </div>
      </div>
      <button className="link-copy" onClick={onCopy} aria-label={`Скопировать: ${label}`}>
        <Copy />
      </button>
    </div>
  )
}
