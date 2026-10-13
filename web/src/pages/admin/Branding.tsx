import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { api, get, put } from '../../lib/api'
import { useApp } from '../../lib/app'
import { applyBranding } from '../../lib/theme'
import { ErrorBox, Field, Spinner, useAction, useLoad, useToast } from '../../ui/kit'

interface B {
  title: string
  tagline: string
  logo_file: string
  primary: string
  background: string
  surface: string
  text: string
  muted: string
  card_opacity: number
  card_blur: number
  bg_mobile_file: string
  bg_desktop_file: string
  bg_fit: string
  bg_opacity: number
  banner_file: string
  banner_link: string
  login_text: string
  pwa_name: string
  pwa_short_name: string
  pwa_icon_file: string
  effect: string
}

const colorFields: [keyof B, string][] = [
  ['primary', 'Основной'],
  ['background', 'Фон'],
  ['surface', 'Карточки'],
  ['text', 'Текст'],
  ['muted', 'Подсказки'],
]

function FilePick({ label, hint, value, accept, onChange }: { label: string; hint?: string; value: string; accept: string; onChange: (id: string) => void }) {
  const ref = useRef<HTMLInputElement>(null)
  const { busy, run } = useAction()
  const [isVideo, setIsVideo] = useState(false)
  const upload = (f: File) =>
    run(async () => {
      const fd = new FormData()
      fd.append('file', f)
      const r = await api<{ id: string; mime: string }>('POST', '/api/admin/files', fd)
      setIsVideo(r.mime.startsWith('video/'))
      onChange(r.id)
    }, 'Файл загружен')
  return (
    <Field label={label} hint={hint}>
      <div className="file-tile">
        <div className="thumb">{value ? isVideo ? <video src={`/files/${value}`} muted /> : <img src={`/files/${value}`} alt="" onError={() => setIsVideo(true)} /> : <span className="tiny muted">нет</span>}</div>
        <div className="row wrap grow">
          <button className="btn secondary small" disabled={busy} onClick={() => ref.current?.click()}>
            {busy ? 'Загрузка…' : value ? 'Заменить' : 'Загрузить'}
          </button>
          {value && (
            <button className="btn ghost small" onClick={() => onChange('')}>
              Убрать
            </button>
          )}
        </div>
        <input ref={ref} type="file" accept={accept} hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
      </div>
    </Field>
  )
}

function Preview({ b }: { b: B }) {
  const style = {
    '--accent': b.primary,
    '--bg': b.background,
    '--surface-solid': b.surface,
    '--text': b.text,
    '--muted': b.muted,
    '--card-alpha': `${b.card_opacity}%`,
    '--card-blur': `${b.card_blur}px`,
    color: b.text,
  } as CSSProperties
  return (
    <div className="preview-phone" style={style} aria-label="Предпросмотр">
      {b.bg_mobile_file && <img src={`/files/${b.bg_mobile_file}`} alt="" style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: b.bg_fit as any, opacity: b.bg_opacity / 100 }} />}
      <div style={{ position: 'relative', padding: 14, fontSize: 13 }}>
        <div className="brand" style={{ marginBottom: 12 }}>
          <img src={b.logo_file ? `/files/${b.logo_file}` : '/icon.svg'} alt="" style={{ width: 34, height: 34, borderRadius: 10 }} />
          <div>
            <div className="brand-name" style={{ fontSize: 15 }}>{b.title}</div>
            <div className="brand-tag" style={{ fontSize: 11 }}>{b.tagline}</div>
          </div>
        </div>
        <div className="ticket" style={{ padding: 14 }}>
          <div className="plan" style={{ fontSize: 13 }}>Бобрёнок</div>
          <div className="left" style={{ margin: '10px 0 2px' }}>
            <b style={{ fontSize: 44 }}>23</b>
            <span style={{ fontSize: 14 }}>дня</span>
          </div>
          <div className="until" style={{ fontSize: 11 }}>до 5 ноября 2026 г.</div>
          <div className="perf" style={{ margin: '12px -14px 10px' }} />
          <div className="stats" style={{ gap: 8 }}>
            <div>
              <div className="stat-label" style={{ fontSize: 10 }}>Трафик</div>
              <div style={{ fontSize: 12 }}>12,4 ГБ</div>
            </div>
            <div>
              <div className="stat-label" style={{ fontSize: 10 }}>Устройства</div>
              <div style={{ fontSize: 12 }}>2 из 3</div>
            </div>
          </div>
        </div>
        <div className="actions" style={{ marginTop: 10, gap: 6 }}>
          {['Продлить', 'Установить', 'Устройства'].map((t) => (
            <span key={t} className="btn secondary" style={{ minHeight: 44, fontSize: 11 }}>
              {t}
            </span>
          ))}
        </div>
        <span className="btn block" style={{ marginTop: 10, minHeight: 40, fontSize: 13 }}>
          Оплатить
        </span>
      </div>
    </div>
  )
}

export default function BrandingPage() {
  const data = useLoad(() => get<B>('/api/admin/settings/branding'))
  const { config } = useApp()
  const [b, setB] = useState<B | null>(null)
  const { busy, run } = useAction()
  const toast = useToast()
  const importRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (data.data) setB(structuredClone(data.data))
  }, [data.data])

  if (data.error) return <ErrorBox text={data.error} retry={data.reload} />
  if (!b)
    return (
      <div className="center" style={{ padding: 40 }}>
        <Spinner />
      </div>
    )
  const set = <K extends keyof B>(k: K, v: B[K]) => setB({ ...b, [k]: v })
  const dirty = JSON.stringify(b) !== JSON.stringify(data.data)

  const save = () =>
    run(async () => {
      const saved = await put<B>('/api/admin/settings/branding', b)
      data.setData(saved)
      applyBranding({
        ...config.branding,
        title: saved.title,
        colors: { primary: saved.primary, background: saved.background, surface: saved.surface, text: saved.text, muted: saved.muted },
        card_opacity: saved.card_opacity,
        card_blur: saved.card_blur,
      })
    }, 'Оформление сохранено. Клиенты увидят его при следующем открытии.')

  const exportScheme = () => {
    const scheme = Object.fromEntries(colorFields.map(([k]) => [k, b[k]]).concat([['card_opacity', b.card_opacity], ['card_blur', b.card_blur]]))
    const blob = new Blob([JSON.stringify(scheme, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'color-scheme.json'
    a.click()
  }
  const importScheme = async (f: File) => {
    try {
      const s = JSON.parse(await f.text())
      setB({ ...b, ...Object.fromEntries(Object.entries(s).filter(([k]) => k in b)) })
      toast('Схема загружена — проверьте и сохраните')
    } catch {
      toast('Файл не похож на схему цветов', true)
    }
  }

  return (
    <div className="stack lg">
      <div className="admin-head" style={{ marginBottom: 0 }}>
        <h1>Оформление</h1>
        <div className="row">
          <button className="btn ghost small" onClick={exportScheme}>
            Экспорт цветов
          </button>
          <button className="btn ghost small" onClick={() => importRef.current?.click()}>
            Импорт цветов
          </button>
          <input ref={importRef} type="file" accept="application/json" hidden onChange={(e) => e.target.files?.[0] && importScheme(e.target.files[0])} />
        </div>
      </div>

      <div className="row" style={{ alignItems: 'flex-start', gap: 24, flexWrap: 'wrap' }}>
        <div className="stack lg grow" style={{ minWidth: 300, maxWidth: 640 }}>
          <section className="card stack">
            <h3>Название и логотип</h3>
            <div className="form-grid">
              <Field label="Название">
                <input className="input" value={b.title} onChange={(e) => set('title', e.target.value)} />
              </Field>
              <Field label="Слоган">
                <input className="input" value={b.tagline} onChange={(e) => set('tagline', e.target.value)} />
              </Field>
            </div>
            <FilePick label="Логотип" hint="Квадратный PNG, SVG или WebP" value={b.logo_file} accept="image/*" onChange={(v) => set('logo_file', v)} />
          </section>

          <section className="card stack">
            <h3>Цвета</h3>
            <div className="row wrap" style={{ gap: 16 }}>
              {colorFields.map(([k, label]) => (
                <label key={k} className="stack" style={{ gap: 4, alignItems: 'center' }}>
                  <input type="color" className="swatch" value={b[k] as string} onChange={(e) => set(k, e.target.value as any)} />
                  <span className="tiny">{label}</span>
                  <span className="tiny muted num">{b[k] as string}</span>
                </label>
              ))}
            </div>
            <Field label={`Непрозрачность карточек: ${b.card_opacity}%`}>
              <input type="range" min={0} max={100} value={b.card_opacity} onChange={(e) => set('card_opacity', Number(e.target.value))} />
            </Field>
            <Field label={`Размытие под карточками: ${b.card_blur}px`}>
              <input type="range" min={0} max={30} value={b.card_blur} onChange={(e) => set('card_blur', Number(e.target.value))} />
            </Field>
          </section>

          <section className="card stack">
            <h3>Фон кабинета</h3>
            <FilePick label="Для телефона" hint="Картинка или видео MP4/WebM, вертикальная" value={b.bg_mobile_file} accept="image/*,video/mp4,video/webm" onChange={(v) => set('bg_mobile_file', v)} />
            <FilePick label="Для компьютера" hint="Если не задан — используется фон для телефона" value={b.bg_desktop_file} accept="image/*,video/mp4,video/webm" onChange={(v) => set('bg_desktop_file', v)} />
            <div className="form-grid">
              <Field label="Заполнение">
                <select className="input" value={b.bg_fit} onChange={(e) => set('bg_fit', e.target.value)}>
                  <option value="cover">Заполнить экран</option>
                  <option value="contain">Целиком</option>
                </select>
              </Field>
              <Field label={`Видимость фона: ${b.bg_opacity}%`}>
                <input type="range" min={0} max={100} value={b.bg_opacity} onChange={(e) => set('bg_opacity', Number(e.target.value))} />
              </Field>
              <Field label="Сезонный эффект">
                <select className="input" value={b.effect} onChange={(e) => set('effect', e.target.value)}>
                  <option value="none">Нет</option>
                  <option value="snow">Снег</option>
                  <option value="leaves">Листопад</option>
                  <option value="halloween">Хэллоуин</option>
                </select>
              </Field>
            </div>
          </section>

          <section className="card stack">
            <h3>Баннер над подпиской</h3>
            <FilePick label="Картинка или видео" hint="Для акций и новостей. Пусто — баннера нет" value={b.banner_file} accept="image/*,video/mp4,video/webm" onChange={(v) => set('banner_file', v)} />
            <Field label="Ссылка по нажатию">
              <input className="input" placeholder="https://t.me/…" value={b.banner_link} onChange={(e) => set('banner_link', e.target.value)} />
            </Field>
          </section>

          <section className="card stack">
            <h3>Страница входа и иконка</h3>
            <Field label="Текст на странице входа">
              <textarea className="input" value={b.login_text} onChange={(e) => set('login_text', e.target.value)} />
            </Field>
            <div className="form-grid">
              <Field label="Название приложения" hint="При добавлении на рабочий стол">
                <input className="input" value={b.pwa_name} onChange={(e) => set('pwa_name', e.target.value)} />
              </Field>
              <Field label="Короткое название">
                <input className="input" value={b.pwa_short_name} onChange={(e) => set('pwa_short_name', e.target.value)} />
              </Field>
            </div>
            <FilePick label="Иконка приложения" hint="PNG 512×512" value={b.pwa_icon_file} accept="image/png,image/webp" onChange={(v) => set('pwa_icon_file', v)} />
          </section>
        </div>

        <div style={{ position: 'sticky', top: 20 }}>
          <Preview b={b} />
        </div>
      </div>

      <div className="save-bar">
        <button className="btn secondary" disabled={!dirty} onClick={() => setB(structuredClone(data.data!))}>
          Отменить
        </button>
        <button className="btn" disabled={busy || !dirty} onClick={save}>
          Сохранить оформление
        </button>
      </div>
    </div>
  )
}
