export function rub(kopecks: number): string {
  const r = kopecks / 100
  const s = Number.isInteger(r) ? r.toLocaleString('ru-RU') : r.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  return `${s} ₽`
}

/** Rounded to whole rubles, for "per month" hints. */
export function rubRound(kopecks: number): string {
  return `${Math.round(kopecks / 100).toLocaleString('ru-RU')} ₽`
}

export function plural(n: number, one: string, few: string, many: string): string {
  const a = Math.abs(n) % 100
  const b = a % 10
  if (a > 10 && a < 20) return many
  if (b === 1) return one
  if (b >= 2 && b <= 4) return few
  return many
}

export function periodLabel(unit: string, amount: number): string {
  if (unit === 'day') return `${amount} ${plural(amount, 'день', 'дня', 'дней')}`
  if (unit === 'week') return `${amount} ${plural(amount, 'неделя', 'недели', 'недель')}`
  if (amount === 12) return '1 год'
  return `${amount} ${plural(amount, 'месяц', 'месяца', 'месяцев')}`
}

export function date(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' })
}

export function shortDate(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: '2-digit', hour: '2-digit', minute: '2-digit' })
}

/** Time left until iso, as a number + unit for big display. */
/** 05.10.2026, 11:28 */
export function dateTimeFull(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}

export function timeLeft(iso: string): { value: number; unit: string; expired: boolean } {
  const ms = new Date(iso).getTime() - Date.now()
  if (ms <= 0) return { value: 0, unit: 'дней', expired: true }
  const days = Math.floor(ms / 86400000)
  if (days >= 1) return { value: days, unit: plural(days, 'день', 'дня', 'дней'), expired: false }
  const hours = Math.max(1, Math.floor(ms / 3600000))
  return { value: hours, unit: plural(hours, 'час', 'часа', 'часов'), expired: false }
}

export function bytes(n: number): string {
  if (n <= 0) return '0 ГБ'
  const gb = n / 1024 ** 3
  if (gb >= 100) return `${Math.round(gb)} ГБ`
  if (gb >= 1) return `${gb.toFixed(1).replace('.', ',')} ГБ`
  return `${Math.round(n / 1024 ** 2)} МБ`
}

export function relative(iso: string | null | undefined): string {
  if (!iso) return 'никогда'
  const s = (Date.now() - new Date(iso).getTime()) / 1000
  if (s < 90) return 'только что'
  if (s < 3600) return `${Math.round(s / 60)} мин назад`
  if (s < 86400) return `${Math.round(s / 3600)} ч назад`
  return shortDate(iso)
}

export const providerName: Record<string, string> = {
  telegram: 'Telegram',
  email: 'Почта',
  yandex: 'Яндекс ID',
  vk: 'VK ID',
}

export const paymentStatus: Record<string, string> = {
  pending: 'Ожидает оплаты',
  succeeded: 'Оплачен',
  canceled: 'Отменён',
  refunded: 'Возврат',
}

export const operationName: Record<string, string> = {
  buy: 'Покупка',
  renew: 'Продление',
  autopay: 'Автопродление',
  change: 'Смена тарифа',
  devices: 'Устройства',
  traffic: 'Трафик',
}
