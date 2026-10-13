import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { api, get, put } from '../../lib/api'
import { useApp } from '../../lib/app'
import { applyBranding } from '../../lib/theme'
import { Calendar, Chevron, ChevronDown, Copy, Gear, Link as LinkIcon, Pencil, Plug, Swap } from '../../ui/icons'
import { ErrorBox, Field, Spinner, Toggle, useAction, useLoad, useToast } from '../../ui/kit'

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
  show_header: boolean
  hero_space: number
  bg_dim: number
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
      {<img src={b.bg_mobile_file ? `/files/${b.bg_mobile_file}` : '/bg-default.webp'} alt="" style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: b.bg_fit as any, opacity: b.bg_opacity / 100 }} />}
      <div style={{ position: 'absolute', inset: 0, background: `linear-gradient(180deg, color-mix(in srgb, ${b.background} ${b.bg_dim}%, transparent), color-mix(in srgb, ${b.background} ${b.bg_dim}%, transparent) 45%, ${b.background})` }} />
      {/* The real home markup at phone size (396 px), scaled into the frame: what you see is what clients get. */}
      <div className="preview-screen">
        <div className="sub-picker-row">
          <span className="sub-picker">
            Подписка 1 <ChevronDown />
          </span>
        </div>
        <div className="home-fill" />
        <div className="carousel-wrap">
          <section className="glass sub-card">
            <div className="sub-head">
              <span className="sub-name">Бобрёнок</span>
              <span className="sub-date">
                до <b>05.11.2026</b>
                <Calendar />
              </span>
            </div>
            <div className="sub-meta">
              <span className="dev-pill">
                2/3 устройства <Pencil />
              </span>
            </div>
            <div className="link-row">
              <span className="link-ico">
                <LinkIcon />
              </span>
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="link-label">Ссылка подписки</div>
                <div className="link-url">
                  <span className="u">https://sub.example.com/abc</span>
                  <span className="link-mask">••••••••</span>
                </div>
              </div>
              <span className="link-copy">
                <Copy />
              </span>
            </div>
            <div className="sub-actions">
              <span className="pill-btn">
                <Plug /> Продлить
              </span>
              <span className="pill-btn">
                <Gear /> Установить и настроить
              </span>
              <span className="pill-btn">
                <Swap /> Сменить тариф
              </span>
            </div>
          </section>
          <span className="car-arrow right">
            <Chevron />
          </span>
        </div>
        <div className="dots-row">
          <div className="dots">
            <span className="dot on" />
            <span className="dot" />
            <span className="dot plus">+</span>
          </div>
        </div>
        <div className="preview-tabbar" />
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
            <Toggle checked={b.show_header} onChange={(v) => set('show_header', v)} label="Показывать логотип и название над содержимым" hint="Выключите, если бренд уже есть на фоновой картинке" />
            <FilePick label="Логотип" hint="Квадратный PNG, SVG или WebP. Не задан — щит BobrGO" value={b.logo_file} accept="image/*" onChange={(v) => set('logo_file', v)} />
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
            <FilePick label="Для телефона" hint="Картинка или видео MP4/WebM, вертикальная. Не задан — стандартный фон BobrGO" value={b.bg_mobile_file} accept="image/*,video/mp4,video/webm" onChange={(v) => set('bg_mobile_file', v)} />
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
              <Field label={`Затемнение фона: ${b.bg_dim}%`}>
                <input type="range" min={0} max={90} value={b.bg_dim} onChange={(e) => set('bg_dim', Number(e.target.value))} />
              </Field>
              <Field label={`Отступ сверху: ${b.hero_space}px`} hint="Для страницы входа. На главной карточка подписки сама прижимается к низу экрана, логотип на фоне остаётся виден">
                <input type="range" min={0} max={600} step={10} value={b.hero_space} onChange={(e) => set('hero_space', Number(e.target.value))} disabled={b.show_header} />
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
            <FilePick label="Иконка приложения" hint="PNG 512×512. Не задана — щит BobrGO" value={b.pwa_icon_file} accept="image/png,image/webp" onChange={(v) => set('pwa_icon_file', v)} />
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
