import { useEffect, useState } from 'react'
import { get, getToken, type Badges } from './api'

// One poller shared by every component that shows counters.
let current: Badges = { news: 0, tickets: 0 }
const subs = new Set<(b: Badges) => void>()
let timer: ReturnType<typeof setInterval> | null = null

export async function refreshBadges() {
  if (!getToken()) return
  try {
    current = await get<Badges>('/api/badges')
    subs.forEach((f) => f(current))
  } catch {
    /* offline: keep old counters */
  }
}

export function useBadges(): Badges {
  const [b, setB] = useState(current)
  useEffect(() => {
    subs.add(setB)
    if (!timer) {
      refreshBadges()
      timer = setInterval(() => document.visibilityState === 'visible' && refreshBadges(), 60000)
    }
    return () => {
      subs.delete(setB)
      if (subs.size === 0 && timer) {
        clearInterval(timer)
        timer = null
      }
    }
  }, [])
  return b
}
