import { useState } from 'react'
import { del, get, post, put } from '../../lib/api'
import { Pencil, Plus, Trash } from '../../ui/icons'
import { ErrorBox, Field, Sheet, Spinner, Toggle, useAction, useLoad, useToast } from '../../ui/kit'

type Kind = 'templates' | 'faq' | 'kb'

const meta: Record<Kind, { title: string; about: string; group: string; head: string; text: string; active?: boolean }> = {
  templates: {
    title: 'Шаблоны ответов',
    about: 'Готовые ответы оператора. В чате обращения наберите «/» и начало названия.',
    group: 'category',
    head: 'title',
    text: 'body',
  },
  faq: {
    title: 'Частые вопросы',
    about: 'Показываются клиенту в разделе «Поддержка» до создания обращения. Ассистент тоже опирается на них.',
    group: 'section',
    head: 'question',
    text: 'answer',
    active: true,
  },
  kb: {
    title: 'База знаний для ассистента',
    about: 'Статьи только для ИИ: инструкции, ответы на типичные проблемы, правила сервиса. Клиенты их не видят. В ответ попадают самые подходящие статьи в пределах лимита символов.',
    group: '',
    head: 'title',
    text: 'body',
    active: true,
  },
}

const labels: Record<string, string> = { category: 'Категория', section: 'Раздел', title: 'Название', question: 'Вопрос', body: 'Текст', answer: 'Ответ' }

export default function Knowledge() {
  const [kind, setKind] = useState<Kind | 'proposals'>('templates')
  return (
    <div className="stack lg" style={{ maxWidth: 900 }}>
      <h1>Шаблоны и база знаний</h1>
      <div className="chips">
        {(Object.keys(meta) as Kind[]).map((k) => (
          <button key={k} className={k === kind ? 'chip on' : 'chip'} onClick={() => setKind(k)}>
            {meta[k].title}
          </button>
        ))}
        <button className={kind === 'proposals' ? 'chip on' : 'chip'} onClick={() => setKind('proposals')}>
          Предложения ИИ
        </button>
      </div>
      {kind === 'proposals' ? <Proposals /> : <Editor key={kind} kind={kind} />}
    </div>
  )
}

function Editor({ kind }: { kind: Kind }) {
  const m = meta[kind]
  const list = useLoad(() => get<Record<string, any>[]>(`/api/admin/support/${kind}`), [kind])
  const [edit, setEdit] = useState<Record<string, any> | null>(null)
  const { busy, run } = useAction()

  if (list.error) return <ErrorBox text={list.error} retry={list.reload} />
  if (!list.data)
    return (
      <div className="center">
        <Spinner />
      </div>
    )

  const save = () =>
    run(async () => {
      if (edit!.id) await put(`/api/admin/support/${kind}/${edit!.id}`, edit)
      else await post(`/api/admin/support/${kind}`, edit)
      setEdit(null)
      await list.reload()
    }, 'Сохранено')

  const remove = (id: number) =>
    run(async () => {
      if (!confirm('Удалить?')) return
      await del(`/api/admin/support/${kind}/${id}`)
      await list.reload()
    })

  const fresh = () => ({ id: 0, [m.head]: '', [m.text]: '', ...(m.group ? { [m.group]: '' } : {}), ...(m.active ? { is_active: true } : {}), sort: 0 })

  return (
    <section className="card stack">
      <div className="spread">
        <p className="small muted" style={{ margin: 0 }}>
          {m.about}
        </p>
        <button className="btn small" onClick={() => setEdit(fresh())}>
          <Plus /> Добавить
        </button>
      </div>
      {list.data.length === 0 ? (
        <p className="small muted">Пока пусто.</p>
      ) : (
        <div className="list">
          {list.data.map((x) => (
            <div className="item" key={x.id}>
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="row" style={{ gap: 8 }}>
                  <b className="ellipsis">{x[m.head]}</b>
                  {m.group && x[m.group] && <span className="badge">{x[m.group]}</span>}
                  {m.active && !x.is_active && <span className="badge warn">скрыто</span>}
                </div>
                <div className="tiny muted ellipsis">{x[m.text]}</div>
              </div>
              <button className="icon-btn" onClick={() => setEdit({ ...x })} aria-label="Изменить">
                <Pencil />
              </button>
              <button className="icon-btn" disabled={busy} onClick={() => remove(x.id)} aria-label="Удалить">
                <Trash />
              </button>
            </div>
          ))}
        </div>
      )}
      <Sheet open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? 'Изменить' : 'Добавить'}>
        {edit && (
          <div className="stack">
            {m.group && (
              <Field label={labels[m.group]}>
                <input className="input" value={edit[m.group]} onChange={(e) => setEdit({ ...edit, [m.group]: e.target.value })} />
              </Field>
            )}
            <Field label={labels[m.head]}>
              <input className="input" value={edit[m.head]} onChange={(e) => setEdit({ ...edit, [m.head]: e.target.value })} />
            </Field>
            <Field label={labels[m.text]}>
              <textarea className="input" style={{ minHeight: 180 }} value={edit[m.text]} onChange={(e) => setEdit({ ...edit, [m.text]: e.target.value })} />
            </Field>
            {m.active && <Toggle checked={!!edit.is_active} onChange={(v) => setEdit({ ...edit, is_active: v })} label={kind === 'faq' ? 'Показывать клиентам' : 'Использовать'} />}
            <button className="btn" disabled={busy} onClick={save}>
              Сохранить
            </button>
          </div>
        )}
      </Sheet>
    </section>
  )
}

type Proposal = { id: number; ticket_id: number | null; title: string; body: string; status: 'pending' | 'approved' | 'rejected'; created_at: string }

/** Articles the assistant drafted from solved tickets and your answers; nothing reaches the base without approval. */
function Proposals() {
  const list = useLoad(() => get<Proposal[]>('/api/admin/support/kb-proposals?status=pending'))
  const [edit, setEdit] = useState<Proposal | null>(null)
  const { busy, run } = useAction()

  const toast = useToast()
  const generate = () =>
    run(async () => {
      const r = await post<{ created: number }>('/api/admin/support/kb-proposals/generate')
      await list.reload()
      toast(r.created ? `Новых предложений: ${r.created}` : 'Новых статей нет: подходящих решённых обращений пока не нашлось')
    })
  const decide = (p: Proposal, approve: boolean) =>
    run(async () => {
      await post(`/api/admin/support/kb-proposals/${p.id}`, { approve, title: p.title, body: p.body })
      setEdit(null)
      await list.reload()
    }, approve ? 'Статья добавлена в базу знаний' : 'Предложение отклонено')

  if (list.error) return <ErrorBox text={list.error} retry={list.reload} />
  if (!list.data)
    return (
      <div className="center">
        <Spinner />
      </div>
    )

  return (
    <section className="card stack">
      <div className="spread wrap" style={{ gap: 12 }}>
        <p className="small muted" style={{ margin: 0, flex: '1 1 260px' }}>
          Ассистент читает решённые обращения с вашими ответами и предлагает статьи для базы знаний — без имён и личных данных. В базу попадает только то, что вы
          одобрите. Автоматически — раз в час, если включено в «Поддержка и ИИ».
        </p>
        <button className="btn small" disabled={busy} onClick={generate}>
          Сгенерировать из тикетов
        </button>
      </div>
      {list.data.length === 0 ? (
        <p className="small muted">Пока нет предложений.</p>
      ) : (
        <div className="list">
          {list.data.map((p) => (
            <div className="item" key={p.id}>
              <div className="grow" style={{ minWidth: 0 }}>
                <b className="ellipsis">{p.title}</b>
                <div className="tiny muted ellipsis">{p.body}</div>
                {p.ticket_id && <div className="tiny muted">из обращения #{p.ticket_id}</div>}
              </div>
              <button className="btn small secondary" onClick={() => setEdit({ ...p })}>
                Проверить
              </button>
            </div>
          ))}
        </div>
      )}
      <Sheet open={!!edit} onClose={() => setEdit(null)} title="Предложение статьи">
        {edit && (
          <div className="stack">
            <Field label="Заголовок">
              <input className="input" value={edit.title} onChange={(e) => setEdit({ ...edit, title: e.target.value })} />
            </Field>
            <Field label="Текст статьи">
              <textarea className="input" style={{ minHeight: 220 }} value={edit.body} onChange={(e) => setEdit({ ...edit, body: e.target.value })} />
            </Field>
            <div className="row" style={{ gap: 8, justifyContent: 'flex-end' }}>
              <button className="btn secondary" disabled={busy} onClick={() => decide(edit, false)}>
                Отклонить
              </button>
              <button className="btn" disabled={busy || !edit.title.trim() || !edit.body.trim()} onClick={() => decide(edit, true)}>
                Добавить в базу
              </button>
            </div>
          </div>
        )}
      </Sheet>
    </section>
  )
}
