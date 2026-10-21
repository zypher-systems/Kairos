import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// Our colour tokens, so tailwind-merge knows `text-faint` is a colour and
// `text-[13px]` a size, and a later className wins over a variant class.
const twMerge = extendTailwindMerge({
  extend: {
    theme: {
      color: [
        'bg', 'panel', 'raised', 'line', 'line-soft', 'input', 'ink', 'dim', 'faint',
        'accent', 'accent-soft', 'accent-ink', 'accent-fg', 'cyan', 'cyan-soft', 'cyan-ink',
        'ok', 'ok-soft', 'warn', 'warn-soft', 'danger', 'danger-soft', 'danger-fg', 'pro', 'pro-soft',
        'ok-fg', 'side', 'terminal', 'terminal-ink', 'terminal-dim', 'terminal-err', 'terminal-sys', 'terminal-warn',
      ],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function fmtDate(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}

export function shortID(id: string) {
  return id.slice(0, 8)
}

export function timeAgo(iso: string | null | undefined): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 0) return 'now'
  const s = Math.floor(diff / 1000)
  if (s < 5) return 'now'
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  if (d < 30) return `${d}d ago`
  return new Date(iso).toLocaleDateString()
}

export function fmtTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  })
}

export function fmtDateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  })
}

export function fmtDuration(a: string | null | undefined, b: string | null | undefined): string {
  if (!a || !b) return '—'
  const ms = new Date(b).getTime() - new Date(a).getTime()
  if (ms < 0) return '—'
  if (ms < 1000) return `${ms}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  return `${m}m ${Math.round(s % 60)}s`
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MiB`
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GiB`
}
// fmtDayTime: "Tue 6 Oct, 02:00". One 24-hour format for every upcoming or
// recent moment that needs its day. Pass a job's time zone to show the moment
// as the job sees it; without one it is the browser's time zone.
export function fmtDayTime(when: string | Date | null | undefined, timeZone?: string): string {
  if (!when) return '—'
  const opts: Intl.DateTimeFormatOptions = { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
  try {
    return new Date(when).toLocaleString(undefined, { ...opts, timeZone: timeZone || undefined })
  } catch {
    // not a time zone the browser knows (a half-typed name): fall back
    return new Date(when).toLocaleString(undefined, opts)
  }
}

// targetLabel names where a run happened, in the words the rest of the UI uses.
export function targetLabel(targetType: string): string {
  return { local: 'Kairos server', agent: 'Agent', ssh: 'SSH host', selector: 'Agents by label' }[targetType] ?? targetType
}
