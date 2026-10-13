// Small stroke icon set (24x24, currentColor).
import type { SVGProps } from 'react'

const base = { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round', strokeLinejoin: 'round' } as const
type P = SVGProps<SVGSVGElement>
const I = (d: string | string[]) => (p: P) => (
  <svg {...base} {...p} aria-hidden="true">
    {(Array.isArray(d) ? d : [d]).map((x, i) => <path key={i} d={x} />)}
  </svg>
)

export const Home = I(['M3 10.5 12 3l9 7.5', 'M5 9.5V21h14V9.5', 'M10 21v-6h4v6'])
export const Chat = I('M12 4c4.97 0 9 3.36 9 7.5S16.97 19 12 19c-1.1 0-2.15-.16-3.12-.46L4 20l1.4-3.6C3.9 15.1 3 13.4 3 11.5 3 7.36 7.03 4 12 4Z')
export const User = I(['M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z', 'M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6'])
export const Shield = I('M12 3 4.5 6v6c0 4.6 3.2 7.7 7.5 9 4.3-1.3 7.5-4.4 7.5-9V6z')
export const Renew = I(['M20 11a8 8 0 0 0-14.5-4.5L4 8', 'M4 3v5h5', 'M4 13a8 8 0 0 0 14.5 4.5L20 16', 'M20 21v-5h-5'])
export const Download = I(['M12 3v12', 'm7 10 5 5 5-5', 'M5 21h14'])
export const Swap = I(['M7 4 3 8l4 4', 'M3 8h14', 'm17 20 4-4-4-4', 'M21 16H7'])
export const Copy = I(['M9 9h11v11H9z', 'M5 15H4V4h11v1'])
export const Phone = I(['M7 2h10v20H7z', 'M11 18h2'])
export const Trash = I(['M4 7h16', 'M10 11v6', 'M14 11v6', 'M6 7l1 13h10l1-13', 'M9 7V4h6v3'])
export const Chevron = I('m9 6 6 6-6 6')
export const Back = I('m15 6-6 6 6 6')
export const Plus = I(['M12 5v14', 'M5 12h14'])
export const Close = I(['M6 6l12 12', 'M18 6 6 18'])
export const Menu = I(['M4 7h16', 'M4 12h16', 'M4 17h16'])
export const Card = I(['M3 6h18v12H3z', 'M3 10h18'])
export const Link = I(['M10 14a4 4 0 0 0 6 0l3-3a4 4 0 0 0-6-6l-1 1', 'M14 10a4 4 0 0 0-6 0l-3 3a4 4 0 0 0 6 6l1-1'])
export const Gift = I(['M4 11h16v10H4z', 'M3 7h18v4H3z', 'M12 7v14', 'M12 7c-2-4-6-3-5 0', 'M12 7c2-4 6-3 5 0'])
export const Tag = I(['M3 12V4h8l10 10-8 8z', 'M8 8h.01'])
export const Users = I(['M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Z', 'M2 20c1-3.5 3.8-5 7-5s6 1.5 7 5', 'M16 4.5a3.5 3.5 0 0 1 0 6.5', 'M18 15c2 .7 3.3 2.3 4 5'])
export const List = I(['M8 6h13', 'M8 12h13', 'M8 18h13', 'M3 6h.01', 'M3 12h.01', 'M3 18h.01'])
export const Settings = I(['M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z', 'M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1Z'])
export const Palette = I(['M12 3a9 9 0 0 0 0 18c1.1 0 1.5-.7 1.5-1.5 0-1.2-.8-1.5-.8-2.5s.7-1.5 1.8-1.5H17a4 4 0 0 0 4-4c0-4.7-4-8.5-9-8.5Z', 'M7.5 11h.01', 'M10 7.5h.01', 'M14.5 7.5h.01'])
export const Wallet = I(['M3 7h16a2 2 0 0 1 2 2v9H3z', 'M3 7l13-3v3', 'M16 13h.01'])
export const Exit = I(['M15 4h4v16h-4', 'M10 8l-4 4 4 4', 'M6 12h11'])
export const Check = I('m5 12 5 5 9-10')
export const Help = I(['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z', 'M9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.7v.5', 'M12 17h.01'])
export const News = I(['M4 5h13v14H6a2 2 0 0 1-2-2z', 'M17 9h3v8a2 2 0 0 1-2 2', 'M8 9h5', 'M8 13h5'])
export const ChevronDown = I('m6 9 6 6 6-6')
export const Calendar = I(['M4 6h16v15H4z', 'M4 10h16', 'M8 3v4', 'M16 3v4', 'M8 14h2', 'M14 14h2'])
export const Pencil = I(['M4 20h4L19 9l-4-4L4 16z', 'M13.5 6.5l4 4'])
export const Devices = I(['M3 5h13v9H3z', 'M7 18h5', 'M9.5 14v4', 'M15 9h6v12h-6z', 'M17.5 18h1'])
export const Gear = Settings
export const Plug = I(['M9 7V3', 'M15 7V3', 'M6 7h12v4a6 6 0 0 1-12 0z', 'M12 17v4'])
export const HomeFill = (p: P) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...p} aria-hidden="true">
    <path d="M12 3.2 2.8 11a1 1 0 0 0 1.3 1.5L5 11.8V20a1 1 0 0 0 1 1h4v-6h4v6h4a1 1 0 0 0 1-1v-8.2l.9.7a1 1 0 0 0 1.3-1.5z" />
  </svg>
)
export const Chart = I(['M4 20h16', 'M7 16v-5', 'M12 16V6', 'M17 16v-8'])
