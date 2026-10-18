// Thin client for the Go JSON API. The session token lives in localStorage.

const TOKEN_KEY = 'bshop.token'

export function getToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return memToken
  }
}
let memToken: string | null = null
export function setToken(t: string | null) {
  memToken = t
  try {
    if (t) localStorage.setItem(TOKEN_KEY, t)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    /* private mode: keep in memory */
  }
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  const tok = getToken()
  if (tok) headers.Authorization = `Bearer ${tok}`
  let payload: BodyInit | undefined
  if (body instanceof FormData) payload = body
  else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  let res: Response
  try {
    res = await fetch(path, { method, headers, body: payload })
  } catch {
    throw new ApiError(0, 'Нет соединения. Проверьте интернет и попробуйте ещё раз.')
  }
  const text = await res.text()
  let data: any = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = null
  }
  if (!res.ok) {
    if (res.status === 401) setToken(null)
    throw new ApiError(res.status, data?.error || `Ошибка ${res.status}`)
  }
  return data as T
}

export const get = <T>(p: string) => api<T>('GET', p)
export const post = <T>(p: string, b?: unknown) => api<T>('POST', p, b ?? {})
export const put = <T>(p: string, b?: unknown) => api<T>('PUT', p, b ?? {})
export const patch = <T>(p: string, b?: unknown) => api<T>('PATCH', p, b ?? {})
export const del = <T>(p: string) => api<T>('DELETE', p)

// ---- types (mirror Go JSON) ----

export interface Branding {
  title: string
  tagline: string
  logo: string
  colors: { primary: string; background: string; surface: string; text: string; muted: string }
  card_opacity: number
  card_blur: number
  bg_mobile: string
  bg_desktop: string
  bg_fit: string
  bg_opacity: number
  banner: string
  banner_link: string
  login_text: string
  effect: string
  show_header: boolean
  hero_space: number
  bg_dim: number
}

export interface LinkItem {
  title: string
  url: string
  enabled: boolean
  kind: string
}

export interface PublicConfig {
  shop_name: string
  bot_username: string
  auth: { telegram: boolean; email: boolean; yandex: boolean; vk: boolean }
  trial: { enabled: boolean; days: number }
  maintenance: boolean
  payment_methods: { code: string; title: string }[]
  save_card: boolean
  branding: Branding
  links: LinkItem[]
  install: { apps: InstallApp[]; note: string }
}

export interface InstallApp {
  platform: string
  name: string
  store_url: string
  deep_link: string
}

export interface Customer {
  id: number
  telegram_id: number | null
  email: string | null
  username: string | null
  first_name: string | null
  last_name: string | null
  language: string
  is_admin: boolean
  is_blocked: boolean
  bot_blocked: boolean
  personal_discount: number
  tag: string
  notes: string
  ref_code: string | null
  referrer_id: number | null
  trial_used: boolean
  source: string
  created_at: string
  last_seen_at: string | null
  bonus_days: number
}

export interface Identity {
  id: number
  provider: string
  display: string
  created_at: string
  last_login: string | null
}

export interface Me {
  customer: Customer
  identities: Identity[]
  trial_available: boolean
  ref_link: string
  referral_enabled: boolean
  gifts_enabled: boolean
  buy_for_other: boolean
  locations: boolean
}

export interface Session {
  token: string
  customer: Customer
  created: boolean
}

export interface Period {
  id: number
  unit: 'day' | 'week' | 'month'
  amount: number
  price: number
  is_best: boolean
  sort?: number
}

export interface Plan {
  id: number
  name: string
  kind: 'period' | 'traffic'
  description: string
  features: string[]
  sort: number
  is_active: boolean
  is_hidden: boolean
  is_default: boolean
  is_trial: boolean
  renewal_disabled: boolean
  change_from_disabled: boolean
  change_to_disabled: boolean
  conversion_disabled: boolean
  required_plan_id: number | null
  max_per_customer: number
  traffic_limit_gb: number
  device_limit: number
  extra_devices_allowed: boolean
  extra_device_price: number
  max_extra_devices: number
  rw_tag: string
  rw_internal_squads: string[]
  rw_external_squad: string | null
  traffic_reset_strategy: string
  reset_traffic_on_buy: boolean
  trial_days: number
  traffic_topup_enabled: boolean
  traffic_packages: TrafficPackage[]
  traffic_reset_enabled: boolean
  traffic_reset_price: number
  created_at?: string
  periods: Period[]
}

export interface Offer {
  period_id: number
  unit: string
  amount: number
  days: number
  base_price: number
  price: number
  per_month: number
  is_best: boolean
  full_price: number
}

export interface CatalogPlan extends Plan {
  price_per_month: number
  offers: Offer[]
  discount_percent: number
  promo_code: string
}

export interface Subscription {
  id: number
  customer_id: number
  plan_id: number | null
  kind: 'trial' | 'paid' | 'free'
  is_primary: boolean
  title: string
  rw_user_id: number | null
  rw_short_uuid: string | null
  rw_username: string | null
  subscription_url: string
  expires_at: string
  traffic_limit_bytes: number
  device_limit: number
  extra_devices: number
  status: string
  created_at: string
  plan_name: string
  used_traffic_bytes: number
  online_at: string | null
  first_connected_at: string | null
  devices_used: number
  panel_status: string
  can_renew: boolean
  can_change_plan: boolean
  can_reset_traffic: boolean
  traffic_topup: boolean
  traffic_reset: boolean
  extra_traffic_bytes: number
  extra_traffic_until: string | null
  autopay: boolean
  autopay_method_id: number | null
  autopay_period_id: number | null
}

export interface Device {
  hwid: string
  userId: number
  platform: string | null
  osVersion: string | null
  deviceModel: string | null
  userAgent: string | null
  name: string
  app: string
  createdAt: string
  updatedAt: string
}

export interface Payment {
  id: string
  customer_id: number
  subscription_id: number | null
  plan_id: number | null
  operation: string
  title: string
  days: number
  months: number
  amount: number
  base_amount: number
  discount_percent: number
  promo_code: string
  provider: string
  provider_ref: string | null
  method: string
  status: 'pending' | 'succeeded' | 'canceled' | 'refunded'
  created_at: string
  paid_at: string | null
  applied_at: string | null
  receipt_status: string
  receipt_uuid: string
  receipt_error: string
  customer_name?: string | null
  plan_name?: string | null
}

export interface BuyResult {
  payment_id: string
  confirmation_url: string
  amount: number
  applied: boolean
}

export interface CustomerRow extends Customer {
  subs_count: number
  payments_count: number
  paid_total: number
  ref_count: number
  expires_at: string | null
}

export interface Paged<T> {
  items: T[]
  total: number
}

// ---- stage 3 ----

export interface ChangeQuote {
  mode: 'upgrade' | 'convert' | 'downgrade'
  days_before: number
  days_after: number
  amount: number
  base_amount: number
}

export interface ChangeTarget {
  plan_id: number
  name: string
  description: string
  features: string[]
  traffic_limit_gb: number
  device_limit: number
  price_per_month: number
  quotes: ChangeQuote[] | null
  unavailable?: string
}

export interface ChangeInfo {
  subscription_id: number
  plan_id: number
  plan_name: string
  traffic_limit_gb: number
  device_limit: number
  price_per_month: number
  days_left: number
  discount_percent: number
  penalty: number
  blocked?: string
  targets: ChangeTarget[]
}

export interface DevicesOffer {
  allowed: boolean
  reason?: string
  current: number
  extra: number
  max_more: number
  days_left: number
  price_per_month: number
  price_per_device: number
  discount_percent: number
}

export interface PaymentMethodInfo {
  id: number
  provider: string
  title: string
  created_at: string
}

export interface AutopayInfo {
  available: boolean
  enabled: boolean
  method_id: number | null
  methods: PaymentMethodInfo[]
  period_id: number
  days: number
  amount: number
  charge_at: string | null
  reason?: string
}

export interface Redemption {
  id: number
  promo_id: number
  customer_id: number
  status: 'reserved' | 'used'
  payment_id: string | null
  subscription_id: number | null
  days: number
  created_at: string
  used_at: string | null
  code: string
  kind: 'discount' | 'days' | 'unlock'
  value: number
  customer?: string
  plan_ids: number[] | null
}

export interface RedeemResult {
  kind: 'discount' | 'days' | 'unlock'
  value: number
  code: string
  subscription_id?: number
  expires_at?: string
  pending: boolean
  message: string
  plan_ids?: number[]
}

export interface ReferralBonus {
  id: number
  recipient_id: number
  referral_id: number | null
  kind: 'first' | 'recurring' | 'invitee' | 'manual'
  days: number
  subscription_id: number | null
  pending: boolean
  created_at: string
  referral_name: string
}

export interface ReferralInfo {
  enabled: boolean
  link: string
  web_link: string
  stats: { invited: number; paid: number; days: number }
  bonuses: ReferralBonus[]
  description: string
  inviter_days: number
  invitee_days: number
  recurring_percent: number
  tiers: { min_invited: number; days: number }[]
  next_tier?: { min_invited: number; days: number }
  current_days: number
  bonus_days: number
}

export interface Promo {
  id: number
  code: string
  kind: 'discount' | 'days' | 'unlock'
  value: number
  folder: string
  description: string
  is_active: boolean
  max_uses: number
  valid_from: string | null
  valid_until: string | null
  plan_ids: number[]
  new_customers_only: boolean
  customer_id: number | null
  reserve_hours: number
  created_at?: string
  used?: number
  reserved?: number
  state?: 'active' | 'inactive' | 'expired' | 'exhausted' | 'scheduled'
}

export interface PlanChange {
  id: number
  mode: string
  days_before: number
  days_after: number
  created_at: string
  from_name: string
  to_name: string
}

// ---- stage 4 ----

export interface Ticket {
  id: number
  customer_id: number
  subject: string
  status: 'open' | 'waiting' | 'resolved' | 'closed'
  assignee_id: number | null
  tag: string
  source: string
  ai_enabled: boolean
  rating: number | null
  rating_comment: string
  unread_admin: boolean
  unread_customer: boolean
  created_at: string
  updated_at: string
  last_customer_at: string
  first_response_at: string | null
  resolved_at: string | null
  closed_at: string | null
}

export interface TicketMessage {
  id: number
  ticket_id: number
  author: 'customer' | 'operator' | 'ai' | 'system' | 'note' | 'draft'
  author_id: number | null
  body: string
  attachments: string[]
  reply_to: number | null
  confidence: number | null
  draft_status: '' | 'pending' | 'sent' | 'rejected'
  created_at: string
}

export interface TicketView extends Ticket {
  messages: TicketMessage[]
}

export interface AdminTicketView extends TicketView {
  customer: Customer
  subscriptions: Subscription[]
}

export interface TicketRow extends Ticket {
  customer_name: string
  username: string | null
  telegram_id: number | null
  last_body: string
  last_author: string
  pending_draft: boolean
}

export interface FAQItem {
  id: number
  section: string
  question: string
  answer: string
  is_active: boolean
  sort: number
}

export interface SupportTemplate {
  id: number
  category: string
  title: string
  body: string
  sort: number
}

export interface KBArticle {
  id: number
  title: string
  body: string
  is_active: boolean
  updated_at?: string
}

export interface SupportConfig {
  enabled: boolean
  faq: FAQItem[]
  max_files: number
  max_mb: number
  rating: boolean
}

export interface UploadedFile {
  id: string
  name: string
  mime: string
  size: number
  url: string
}

export interface NewsItem {
  id: number
  title: string
  body: string
  image: string
  buttons: { text: string; url: string }[]
  date: string
  unread: boolean
}

export interface Badges {
  news: number
  tickets: number
  admin_tickets?: number
}

export interface Broadcast {
  id: number
  title: string
  body: string
  image: string
  buttons: { text: string; url: string }[]
  audience: { segment: string; plan_id?: number }
  channel: 'telegram' | 'email' | 'all'
  show_in_news: boolean
  status: 'draft' | 'scheduled' | 'sending' | 'done' | 'canceled'
  scheduled_at: string | null
  total: number
  sent: number
  failed: number
  created_at: string
  started_at: string | null
  finished_at: string | null
}

export interface AutoStep {
  step_order: number
  delay_duration: string
  template_id: number | null
  message_content: string
  stop_on_purchase: boolean
  buttons: { text: string; url: string }[]
  repeat_count: number
  repeat_interval: string | null
  slots: unknown[]
  webhook_url: string | null
  webhook_method: string | null
  image?: string
}

export interface Automation {
  id: number
  name: string
  trigger_type: 'event' | 'cron'
  trigger_factor: string
  is_active: boolean
  cooldown_hours: number
  skip_if_active_in: string[]
  schedule_mode: string
  loop_count: number
  shuffle_steps: boolean
  steps: AutoStep[]
  stats?: { active: number; runs: number; sent: number; converted: number }
}

export interface TriggerInfo {
  factor: string
  type: 'event' | 'cron'
  title: string
  about: string
  vars: string[] | null
}

export interface AutomationRun {
  id: number
  automation_id: number
  automation_name: string
  status: 'active' | 'done' | 'stopped'
  stop_reason: string
  position: number
  plan: unknown[]
  next_at: string | null
  sent: number
  converted: boolean
  started_at: string
  finished_at: string | null
}

export interface EventInfo {
  key: string
  title: string
  group: string
  template: string
  vars: string[]
}

export async function uploadFile(file: File, admin = false): Promise<UploadedFile> {
  const fd = new FormData()
  fd.append('file', file)
  return api<UploadedFile>('POST', admin ? '/api/admin/tickets/files' : '/api/files', fd)
}

export interface Gift {
  id: number
  code: string
  plan_id: number | null
  title: string
  days: number
  status: 'ready' | 'redeemed'
  created_at: string
  redeemed_at: string | null
}

export interface TrafficPackage {
  gb: number
  price: number
}

export interface TrafficOffer {
  topup: boolean
  packages: TrafficPackage[]
  reset: boolean
  can_reset: boolean
  reset_price: number
  threshold: number
  limit_bytes: number
  extra_bytes: number
  used_bytes: number
  until: string
  extra_until?: string
  auto_available: boolean
  auto_topup: boolean
  auto_topup_package: number
  auto_topup_at: number
  saved_methods: { id: number; provider: string; title: string }[]
}

export interface Location {
  name: string
  country: string
  online: boolean
}

export interface Banner {
  id: number
  kind: '' | 'link_email' | 'link_telegram'
  title: string
  subtitle: string
  icon: string
  segment: 'all' | 'active' | 'trial' | 'expired' | 'no_sub'
  button_text: string
  button_url: string
  starts_at: string | null
  ends_at: string | null
  is_active: boolean
  dismissible: boolean
  sort: number
}
