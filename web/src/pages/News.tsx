import DOMPurify from 'dompurify'
import { useNavigate } from 'react-router-dom'
import { get, type NewsItem } from '../lib/api'
import { date } from '../lib/format'
import { openLink } from '../lib/tg'
import { Back, News as NewsIcon } from '../ui/icons'
import { ErrorBox, PageLoader, useLoad } from '../ui/kit'

const tags = ['b', 'strong', 'i', 'em', 'u', 'ins', 's', 'strike', 'del', 'code', 'pre', 'a', 'blockquote', 'br']

export function safeHTML(html: string) {
  return DOMPurify.sanitize(html.replace(/\n/g, '<br>'), { ALLOWED_TAGS: tags, ALLOWED_ATTR: ['href'] })
}

export default function News() {
  const nav = useNavigate()
  const news = useLoad(() => get<NewsItem[]>('/api/news'))
  if (news.loading && !news.data) return <PageLoader />
  if (news.error) return <ErrorBox text={news.error} retry={news.reload} />
  const items = news.data || []

  const go = (url: string) => {
    if (url.startsWith('/')) nav(url)
    else if (url.startsWith(location.origin)) nav(url.slice(location.origin.length) || '/')
    else openLink(url)
  }

  return (
    <div className="stack lg">
      <div className="row">
        <button className="icon-btn" onClick={() => nav(-1)} aria-label="Назад">
          <Back />
        </button>
        <h1>Новости</h1>
      </div>
      {items.length === 0 ? (
        <div className="card empty">
          <NewsIcon style={{ width: 36, height: 36, color: 'var(--accent)' }} />
          <p className="small">Новостей пока нет.</p>
        </div>
      ) : (
        items.map((n) => (
          <article key={n.id} className="card news-item stack">
            {n.image && <img className="cover" src={n.image} alt="" loading="lazy" />}
            {(n.title || n.unread) && (
              <div className="spread">
                {n.title && <h3 className="news-title">{n.title}</h3>}
                {n.unread && <span className="badge accent">новое</span>}
              </div>
            )}
            <div className="body" dangerouslySetInnerHTML={{ __html: safeHTML(n.body) }} />
            {n.buttons?.length > 0 && (
              <div className="row wrap">
                {n.buttons.map((b, i) => (
                  <button key={i} className="btn secondary small" onClick={() => go(b.url)}>
                    {b.text}
                  </button>
                ))}
              </div>
            )}
            <span className="tiny muted news-date">{date(n.date)}</span>
          </article>
        ))
      )}
    </div>
  )
}
