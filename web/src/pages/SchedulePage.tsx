import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Cron } from 'croner'
import { CalendarRange } from 'lucide-react'
import { useJobs, useRuns, useWorkflows } from '@/hooks/queries'
import { EmptyState, Tabs, statusColor } from '@/components/ui'
import { cn, fmtTime } from '@/lib/utils'
import type { Job, Run } from '@/lib/types'

type RangeKey = '24h' | '7d'
const RANGE_MS: Record<RangeKey, number> = { '24h': 24 * 3600_000, '7d': 7 * 24 * 3600_000 }
const MAX_TICKS = 60

interface Tick {
  t: number
}

/** Future fire-times for a job inside [from, to]. */
type Scheduled = Pick<Job, 'schedule_type' | 'cron_expr' | 'interval_seconds' | 'oneoff_at' | 'timezone' | 'next_run_at' | 'enabled'>

function futureTicks(job: Scheduled, from: number, to: number): { ticks: Tick[]; hidden: number } {
  const out: Tick[] = []
  let hidden = 0
  try {
    switch (job.schedule_type) {
      case 'cron': {
        const cron = new Cron(job.cron_expr, { timezone: job.timezone, paused: false })
        const cap = 400
        for (const d of cron.nextRuns(cap, new Date(from))) {
          const t = d.getTime()
          if (t > to) break
          if (out.length < MAX_TICKS) out.push({ t })
          else hidden++
        }
        break
      }
      case 'interval': {
        const step = job.interval_seconds * 1000
        if (step < 5000) break
        let t = job.next_run_at ? new Date(job.next_run_at).getTime() : from + step
        while (t - step > from) t -= step
        for (; t <= to; t += step) {
          if (t < from) continue
          if (out.length < MAX_TICKS) out.push({ t })
          else hidden++
        }
        break
      }
      case 'oneoff': {
        if (job.oneoff_at) {
          const t = new Date(job.oneoff_at).getTime()
          if (t >= from && t <= to) out.push({ t })
        }
        break
      }
    }
  } catch {
    /* unparseable schedule — no ticks */
  }
  return { ticks: out, hidden }
}

function axisMarks(from: number, to: number, range: RangeKey): { t: number; label: string }[] {
  const marks: { t: number; label: string }[] = []
  const step = range === '24h' ? 4 * 3600_000 : 24 * 3600_000
  const d = new Date(from)
  if (range === '24h') {
    d.setMinutes(0, 0, 0)
    d.setHours(d.getHours() + 1)
  } else {
    d.setHours(0, 0, 0, 0)
    d.setDate(d.getDate() + 1)
  }
  for (let t = d.getTime(); t <= to; t += step) {
    const dd = new Date(t)
    marks.push({
      t,
      label:
        range === '24h'
          ? dd.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
          : dd.toLocaleDateString(undefined, { weekday: 'short', month: 'numeric', day: 'numeric' }),
    })
  }
  return marks
}

export default function SchedulePage() {
  const [range, setRange] = useState<RangeKey>('24h')
  const { data: jobs } = useJobs()
  const { data: workflows } = useWorkflows()
  const { data: runs } = useRuns({ limit: '100' })
  const [now, setNow] = useState(Date.now())

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(t)
  }, [])

  const window = RANGE_MS[range]
  const from = now - window / 4
  const to = now + (window * 3) / 4
  const span = to - from
  const pct = (t: number) => ((t - from) / span) * 100

  const marks = useMemo(() => axisMarks(from, to, range), [from, to, range])

  const lanes = useMemo(() => {
    const pastByJob = new Map<string, Run[]>()
    for (const r of runs ?? []) {
      const t = new Date(r.started_at ?? r.queued_at).getTime()
      if (t >= from && t <= now) {
        const list = pastByJob.get(r.job_id) ?? []
        list.push(r)
        pastByJob.set(r.job_id, list)
      }
    }
    // Workflows with their own schedule get a lane too; their runs are
    // charted on the jobs they start.
    const wfLanes = (workflows ?? [])
      .filter((wf) => wf.schedule_type && wf.schedule_type !== 'manual')
      .map((wf) => {
        const fut = wf.enabled ? futureTicks(wf, now, to) : { ticks: [] as Tick[], hidden: 0 }
        return { id: `wf-${wf.id}`, name: wf.name, to: '/workflows', enabled: wf.enabled, isWorkflow: true, future: fut.ticks, hidden: fut.hidden, past: [] as Run[] }
      })
    const jobLanes = (jobs ?? []).map((job) => {
      const fut = job.enabled ? futureTicks(job, now, to) : { ticks: [] as Tick[], hidden: 0 }
      return {
        id: job.id,
        name: job.name,
        to: `/jobs/${job.id}`,
        enabled: job.enabled,
        isWorkflow: false,
        future: fut.ticks,
        hidden: fut.hidden,
        past: pastByJob.get(job.id) ?? [],
      }
    })
    return [...wfLanes, ...jobLanes]
  }, [jobs, workflows, runs, now, to, from])

  const nowPct = pct(now)

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Schedule</h1>
          <p className="mt-1.5 text-sm text-faint">Every job on one timeline: recent runs on the left of now, upcoming runs on the right.</p>
        </div>
        <Tabs
          label="Time range"
          value={range}
          onChange={(v) => setRange(v as RangeKey)}
          tabs={[
            { value: '24h', label: '24 hours' },
            { value: '7d', label: '7 days' },
          ]}
        />
      </header>

      {lanes.length === 0 ? (
        <div className="rounded-2xl border border-line bg-panel">
          <EmptyState icon={CalendarRange} title="Nothing scheduled" sub="Create a job and its upcoming runs appear on this timeline." />
        </div>
      ) : (
        <section aria-label="Timeline" className="rounded-2xl border border-line bg-panel p-5">
          <div className="overflow-x-auto">
            <div className="flex min-w-[640px]">
              <div className="w-36 shrink-0 sm:w-48">
                <div className="flex h-8 items-end border-b border-line pr-3 pb-1.5 text-xs font-semibold text-faint">Job</div>
                {lanes.map((lane) => (
                  <div key={lane.id} className="flex h-11 items-center gap-2 border-b border-line-soft pr-3">
                    <Link
                      to={lane.to}
                      className={cn('truncate text-sm font-semibold hover:underline', lane.enabled ? 'text-ink' : 'text-faint')}
                    >
                      {lane.name}
                    </Link>
                    {lane.isWorkflow && <span className="shrink-0 rounded bg-raised px-1 text-[10px] font-semibold text-dim uppercase">Workflow</span>}
                  </div>
                ))}
              </div>

              <div className="relative min-w-0 flex-1">
                <div className="relative h-8 border-b border-line">
                  {marks.map((m) => (
                    <div key={m.t} className="absolute bottom-0" style={{ left: `${pct(m.t)}%` }}>
                      <span className="absolute bottom-1.5 -translate-x-1/2 text-xs whitespace-nowrap text-faint tabular-nums">{m.label}</span>
                      <span className="absolute bottom-0 h-1.5 w-px bg-line" />
                    </div>
                  ))}
                </div>

                {lanes.map(({ id, name, enabled, future, past, hidden }) => (
                  <div key={id} className="relative h-11 border-b border-line-soft">
                    {marks.map((m) => (
                      <span key={m.t} className="absolute top-0 h-full w-px bg-line-soft" style={{ left: `${pct(m.t)}%` }} />
                    ))}
                    <span className="absolute inset-x-0 top-1/2 h-px -translate-y-1/2 bg-line-soft" />
                    {past.map((r) => {
                      const sc = statusColor(r.status)
                      const pt = new Date(r.started_at ?? r.queued_at).getTime()
                      return (
                        <Link
                          key={r.id}
                          to={`/runs/${r.id}`}
                          aria-label={`${name}: ${sc.label} at ${fmtTime(r.started_at ?? r.queued_at)}`}
                          title={`${sc.label} · ${fmtTime(r.started_at ?? r.queued_at)}`}
                          style={{ left: `${pct(pt)}%` }}
                          className={cn('absolute top-1/2 h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 transition-transform hover:scale-150', sc.dot)}
                        />
                      )
                    })}
                    {future.map((tick, i) => (
                      <span
                        key={i}
                        title={`Scheduled · ${fmtTime(new Date(tick.t).toISOString())}`}
                        className="absolute top-1/2 h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-[1.5px] border-dashed border-faint bg-panel"
                        style={{ left: `${pct(tick.t)}%` }}
                      />
                    ))}
                    {hidden > 0 && (
                      <span className="absolute top-1/2 right-2 -translate-y-1/2 rounded bg-panel px-1 text-xs text-faint">+{hidden} more</span>
                    )}
                    {!enabled && (
                      <span className="absolute top-1/2 right-2 -translate-y-1/2 rounded bg-panel px-1 text-xs font-semibold text-faint">Paused</span>
                    )}
                  </div>
                ))}

                <div className="pointer-events-none absolute top-0 bottom-0 w-0.5 bg-accent" style={{ left: `${nowPct}%` }}>
                  <span className="absolute -top-1 -translate-x-1/2 rounded bg-accent px-1.5 text-[11px] font-semibold text-accent-fg">Now</span>
                </div>
              </div>
            </div>
          </div>

          <div className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-[13px] text-faint">
            <span className="flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-full bg-ok" aria-hidden="true" /> Succeeded
            </span>
            <span className="flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-[1px] bg-danger" aria-hidden="true" /> Failed
            </span>
            <span className="flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-full bg-cyan" aria-hidden="true" /> Running
            </span>
            <span className="flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-full border-[1.5px] border-dashed border-faint" aria-hidden="true" /> Upcoming
            </span>
            <span className="flex items-center gap-1.5">
              <span className="h-3 w-0.5 bg-accent" aria-hidden="true" /> Now
            </span>
          </div>
        </section>
      )}
    </div>
  )
}
