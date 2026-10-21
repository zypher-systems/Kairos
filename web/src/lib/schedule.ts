import { Cron } from 'croner'
import type { Job } from '@/lib/types'

const DAYS = ['Sundays', 'Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays']
const pad = (n: number) => String(n).padStart(2, '0')

export function fmtEvery(sec: number): string {
  if (sec % 86400 === 0) return sec === 86400 ? 'day' : `${sec / 86400} days`
  if (sec % 3600 === 0) return sec === 3600 ? 'hour' : `${sec / 3600} h`
  if (sec % 60 === 0) return sec === 60 ? 'minute' : `${sec / 60} min`
  return `${sec} s`
}

// describeSchedule turns a job's schedule into a large label and a small
// qualifier ("02:00" / "every day"), falling back to the raw cron expression.
export function describeSchedule(j: Pick<Job, 'schedule_type' | 'cron_expr' | 'interval_seconds' | 'oneoff_at'>): {
  big: string
  small: string
  sentence: string
} {
  if (j.schedule_type === 'manual') {
    return { big: 'Manual', small: 'by hand or workflow', sentence: 'Runs by hand, from a workflow, or from a file trigger' }
  }
  if (j.schedule_type === 'interval') {
    const every = fmtEvery(j.interval_seconds)
    return { big: every.replace(/^(minute|hour|day)$/, '1 $1'), small: 'every', sentence: `Every ${every}` }
  }
  if (j.schedule_type === 'oneoff') {
    if (!j.oneoff_at) return { big: 'Once', small: 'not set', sentence: 'Once' }
    const d = new Date(j.oneoff_at)
    const day = d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
    const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`
    return { big: day, small: `once · ${time}`, sentence: `Once, ${day} at ${time}` }
  }
  const expr = j.cron_expr.trim()
  const f = expr.split(/\s+/)
  if (f.length === 5) {
    const [m, h, dom, mon, dow] = f
    const num = (s: string) => /^\d+$/.test(s)
    const step = (s: string) => /^\*\/\d+$/.test(s) ? Number(s.slice(2)) : null
    if (dom === '*' && mon === '*' && dow === '*') {
      if (step(m) && h === '*') return { big: `${step(m)} min`, small: 'every', sentence: `Every ${step(m)} minutes` }
      if (m === '*' && h === '*') return { big: '1 min', small: 'every', sentence: 'Every minute' }
      if (num(m) && h === '*') return { big: 'hourly', small: m === '0' ? 'on the hour' : `at :${pad(+m)}`, sentence: `Every hour at :${pad(+m)}` }
      if (num(m) && step(h)) return { big: `${step(h)} h`, small: 'every', sentence: `Every ${step(h)} hours` }
      if (num(m) && num(h)) return { big: `${pad(+h)}:${pad(+m)}`, small: 'every day', sentence: `Every day at ${pad(+h)}:${pad(+m)}` }
    }
    if (num(m) && num(h) && dom === '*' && mon === '*') {
      const time = `${pad(+h)}:${pad(+m)}`
      if (dow === '1-5') return { big: time, small: 'weekdays', sentence: `Weekdays at ${time}` }
      if (dow === '0,6' || dow === '6,0') return { big: time, small: 'weekends', sentence: `Weekends at ${time}` }
      if (num(dow) && +dow <= 6) return { big: time, small: DAYS[+dow], sentence: `${DAYS[+dow]} at ${time}` }
    }
    if (num(m) && num(h) && num(dom) && mon === '*' && dow === '*') {
      const time = `${pad(+h)}:${pad(+m)}`
      return { big: time, small: `monthly · day ${dom}`, sentence: `Monthly on day ${dom} at ${time}` }
    }
  }
  return { big: expr, small: 'cron', sentence: `Cron ${expr}` }
}

// scheduleZone is the clock a schedule's times are printed in: the job's
// time zone, except for a one-off, which is picked in the browser (a
// datetime-local field) and stored as an instant. undefined means the
// browser's zone.
export function scheduleZone(j: { schedule_type: string; timezone?: string }): string | undefined {
  return j.schedule_type === 'oneoff' ? undefined : j.timezone || undefined
}

// relFuture: "in 4 min", "in 11 h", "Thu 06:30", "28 Sep". Past a day out it
// prints a clock time, in timeZone when one is given.
export function relFuture(iso: string | null | undefined, now = Date.now(), timeZone?: string): string {
  if (!iso) return '—'
  const t = new Date(iso)
  const diff = t.getTime() - now
  if (diff < 0) return 'due now'
  const min = Math.round(diff / 60_000)
  if (min < 1) return 'in <1 min'
  if (min < 60) return `in ${min} min`
  const h = Math.round(min / 60)
  if (h < 24) return `in ${h} h`
  const fmt = (opts: Intl.DateTimeFormatOptions) => {
    try {
      return t.toLocaleString(undefined, { ...opts, timeZone })
    } catch {
      return t.toLocaleString(undefined, opts)
    }
  }
  if (h < 24 * 6) return `${fmt({ weekday: 'short' })} ${fmt({ hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })}`
  return fmt({ day: 'numeric', month: 'short' })
}

export function clock(iso: string | null | undefined): string {
  if (!iso) return '—'
  const t = new Date(iso)
  return `${pad(t.getHours())}:${pad(t.getMinutes())}`
}

// firingsBetween lists when an enabled job fires in [from, to).
export function firingsBetween(j: Job, from: Date, to: Date, max = 400): Date[] {
  if (!j.enabled) return []
  const out: Date[] = []
  try {
    if (j.schedule_type === 'cron') {
      const c = new Cron(j.cron_expr, { timezone: j.timezone || undefined, paused: true })
      let next = c.nextRun(from)
      while (next && next < to && out.length < max) {
        out.push(next)
        next = c.nextRun(next)
      }
    } else if (j.schedule_type === 'interval' && j.interval_seconds > 0) {
      const step = j.interval_seconds * 1000
      let t = j.next_run_at ? new Date(j.next_run_at).getTime() : from.getTime()
      while (t < from.getTime()) t += step
      for (; t < to.getTime() && out.length < max; t += step) out.push(new Date(t))
    } else if (j.schedule_type === 'oneoff' && j.oneoff_at) {
      const t = new Date(j.oneoff_at)
      if (t >= from && t < to) out.push(t)
    }
  } catch {
    // unparsable schedule: nothing to plot
  }
  return out
}
