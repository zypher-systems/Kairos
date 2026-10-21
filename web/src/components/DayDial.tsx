import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { degOnFace, endOfDay, startOfDay } from '@/lib/dayface'
import { clock, describeSchedule, firingsBetween, relFuture, scheduleZone } from '@/lib/schedule'
import { cn } from '@/lib/utils'
import type { Job, Run } from '@/lib/types'

// The face is a 24-hour clock: midnight at the top, noon at the bottom.
const FACE = 400
const MID = FACE / 2
const BAND_R = 150
const RING_R = [122, 112, 102]
// A job that fires more often than this in a day is drawn as a ticked ring,
// not as one dot per run.
const FREQUENT = 24
// A job with more firings than this in the next 24 hours gets a ring on the
// row-sized face, where that many dots would overlap.
const MINI_FREQUENT = 8
const HOURS_24 = 86_400_000

type MarkKind = 'ok' | 'failed' | 'live' | 'other' | 'due'

const runKind: Record<string, MarkKind> = {
  succeeded: 'ok',
  failed: 'failed',
  running: 'live',
  claimed: 'live',
  queued: 'live',
  pending_approval: 'live',
}

interface DayPlan {
  marks: { key: string; deg: number; kind: MarkKind }[]
  rings: { job: Job; count: number }[]
  moreFrequent: number
  counts: Record<MarkKind, number>
}

function planDay(jobs: Job[], runs: Run[], now: Date): DayPlan {
  const dayStart = startOfDay(now)
  const dayEnd = endOfDay(now)
  const fires = jobs.map((job) => ({ job, fires: firingsBetween(job, dayStart, dayEnd, 1500) }))
  const frequent = fires.filter((f) => f.fires.length > FREQUENT).sort((a, b) => b.fires.length - a.fires.length)
  const frequentIDs = new Set(frequent.map((f) => f.job.id))

  const marks: DayPlan['marks'] = []
  const seen = new Set<string>()
  for (const r of runs) {
    const at = new Date(r.started_at ?? r.queued_at ?? r.created_at)
    if (seen.has(r.id) || at < dayStart || at >= dayEnd) continue
    seen.add(r.id)
    const kind = runKind[r.status] ?? 'other'
    // A frequent job's runs are the ring; only its failures get a mark.
    if (frequentIDs.has(r.job_id) && kind !== 'failed') continue
    marks.push({ key: r.id, deg: degOnFace(at), kind })
  }
  for (const f of fires) {
    if (frequentIDs.has(f.job.id)) continue
    for (const d of f.fires) {
      if (d.getTime() > now.getTime()) marks.push({ key: `${f.job.id}-${d.getTime()}`, deg: degOnFace(d), kind: 'due' })
    }
  }
  // Failures paint last so they are never hidden under another mark.
  const order: MarkKind[] = ['due', 'other', 'ok', 'live', 'failed']
  marks.sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind))
  // Count what is drawn, so the spoken summary matches the face.
  const drawn = marks.slice(-160)
  const counts: Record<MarkKind, number> = { ok: 0, failed: 0, live: 0, other: 0, due: 0 }
  for (const m of drawn) counts[m.kind]++
  return {
    marks: drawn,
    rings: frequent.slice(0, RING_R.length).map((f) => ({ job: f.job, count: f.fires.length })),
    moreFrequent: Math.max(0, frequent.length - RING_R.length),
    counts,
  }
}

function Mark({ kind }: { kind: MarkKind }) {
  const y = MID - BAND_R
  if (kind === 'failed') return <rect x={MID - 7.5} y={y - 7.5} width="15" height="15" rx="3" className="fill-danger stroke-bg" strokeWidth="2" />
  if (kind === 'due') return <circle cx={MID} cy={y} r="5" className="fill-bg stroke-input" strokeWidth="2" />
  return (
    <circle
      cx={MID}
      cy={y}
      r="7"
      className={cn('stroke-bg', kind === 'ok' ? 'fill-ok' : kind === 'live' ? 'fill-accent' : 'fill-faint')}
      strokeWidth="2"
    />
  )
}

// DayDial draws today on a 24-hour face: what ran, what failed, what is still
// to come, and a hand on now. The centre names the next run.
export function DayDial({ jobs, runs, now, className }: { jobs: Job[]; runs: Run[]; now: Date; className?: string }) {
  // Re-plan when the minute changes, not on every render.
  const minute = Math.floor(now.getTime() / 60_000)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const plan = useMemo(() => planDay(jobs, runs, now), [jobs, runs, minute])

  const next = useMemo(() => {
    const upcoming = jobs
      .filter((j) => j.enabled && j.next_run_at)
      .sort((a, b) => new Date(a.next_run_at!).getTime() - new Date(b.next_run_at!).getTime())
    const first = upcoming[0]
    if (!first) return null
    const at = new Date(first.next_run_at!).getTime()
    return { at: first.next_run_at!, jobs: upcoming.filter((j) => Math.abs(new Date(j.next_run_at!).getTime() - at) < 60_000) }
  }, [jobs])

  const nowDeg = degOnFace(now)
  const { counts } = plan
  const summary = [
    `Today on a 24-hour dial, now ${clock(now.toISOString())}.`,
    counts.failed ? `${counts.failed} failed.` : 'No failures.',
    `${counts.ok} succeeded.`,
    counts.due ? `${counts.due} still to run.` : '',
    plan.rings.length ? `Running through the day: ${plan.rings.map((r) => r.job.name).join(', ')}.` : '',
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <div className={cn('flex flex-col items-center gap-4', className)}>
      <div className="relative aspect-square w-full max-w-[400px]">
        <svg viewBox={`0 0 ${FACE} ${FACE}`} fill="none" role="img" aria-label={summary} className="block h-full w-full">
          <circle cx={MID} cy={MID} r={BAND_R} className="stroke-raised" strokeWidth="22" />
          {/* 18:00 to 06:00, over the top */}
          <path d={`M ${MID - BAND_R} ${MID} A ${BAND_R} ${BAND_R} 0 0 1 ${MID + BAND_R} ${MID}`} className="stroke-input/40" strokeWidth="22" />
          <g className="stroke-input" strokeWidth="2" strokeLinecap="round">
            {Array.from({ length: 24 }, (_, h) => (
              <line key={h} x1={MID} y1={h % 6 === 0 ? 22 : 26} x2={MID} y2={h % 6 === 0 ? 34 : 32} transform={`rotate(${h * 15} ${MID} ${MID})`} />
            ))}
          </g>
          <g className="fill-faint text-[12px] font-semibold" textAnchor="middle">
            <text x={MID} y="14">00</text>
            <text x={FACE - 10} y={MID + 4}>06</text>
            <text x={MID} y={FACE - 2}>12</text>
            <text x="10" y={MID + 4}>18</text>
          </g>
          {plan.rings.map((ring, i) => {
            const step = (2 * Math.PI * RING_R[i]) / ring.count
            const dash = Math.min(1.8, step * 0.45)
            return (
              <circle
                key={ring.job.id}
                cx={MID}
                cy={MID}
                r={RING_R[i]}
                className="stroke-accent/50"
                strokeWidth="5"
                strokeDasharray={`${dash} ${step - dash}`}
                transform={`rotate(-90 ${MID} ${MID})`}
              />
            )
          })}
          {plan.marks.map((m) => (
            <g key={m.key} transform={`rotate(${m.deg} ${MID} ${MID})`}>
              <Mark kind={m.kind} />
            </g>
          ))}
          <g transform={`rotate(${nowDeg} ${MID} ${MID})`}>
            <line x1={MID} y1="30" x2={MID} y2="96" className="stroke-accent" strokeWidth="3.5" strokeLinecap="round" />
            <circle cx={MID} cy="24" r="6" className="fill-accent" />
          </g>
        </svg>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-0.5 px-[27%] text-center">
          {next ? (
            <>
              <div className="text-[13px] text-faint">Next up</div>
              <div className="font-display text-[clamp(26px,9vw,40px)] leading-[1.1] font-semibold tracking-[-0.03em] text-ink tabular-nums">
                {clock(next.at)}
              </div>
              <Link to={`/jobs/${next.jobs[0].id}`} className="pointer-events-auto max-w-full truncate font-semibold text-ink hover:underline">
                {next.jobs[0].name}
              </Link>
              {next.jobs.length > 1 && <div className="text-[13px] text-faint">and {next.jobs.length - 1} more</div>}
              <div className="font-semibold text-accent">{relFuture(next.at, now.getTime())}</div>
            </>
          ) : (
            <>
              <div className="text-[13px] text-faint">Next up</div>
              <div className="font-semibold text-ink">Nothing scheduled</div>
            </>
          )}
        </div>
      </div>
      <ul className="flex flex-wrap justify-center gap-x-5 gap-y-1.5 text-[13px] text-faint">
        <li className="flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-full bg-ok" aria-hidden="true" />
          succeeded
        </li>
        <li className="flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-[2px] bg-danger" aria-hidden="true" />
          failed
        </li>
        <li className="flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-full border-2 border-input" aria-hidden="true" />
          still to run
        </li>
        {plan.rings.map((r) => (
          <li key={r.job.id} className="flex items-center gap-2">
            <span className="w-4 border-t-4 border-dotted border-accent/60" aria-hidden="true" />
            {r.job.name}, {describeSchedule(r.job).sentence.toLowerCase()}
          </li>
        ))}
        {plan.moreFrequent > 0 && <li>and {plan.moreFrequent} more running through the day</li>}
      </ul>
    </div>
  )
}

// MiniDial is the same face at row size: a dot at the time of day a job next
// runs, or a ticked ring when it runs all day.
export function MiniDial({ job, className }: { job: Job; className?: string }) {
  const { frequent, degs } = useMemo(() => {
    const from = new Date()
    const fires = firingsBetween(job, from, new Date(from.getTime() + HOURS_24), MINI_FREQUENT + 1)
    if (fires.length > MINI_FREQUENT) return { frequent: true, degs: [] as number[] }
    const times = fires.length ? fires : job.next_run_at ? [new Date(job.next_run_at)] : []
    // The dot follows the same clock as the time printed beside it.
    const zone = scheduleZone(job)
    return { frequent: false, degs: [...new Set(times.map((t) => degOnFace(t, zone)))] }
  }, [job])
  const on = job.enabled
  return (
    <svg width="44" height="44" viewBox="0 0 44 44" fill="none" aria-hidden="true" className={cn('shrink-0', className)}>
      <circle
        cx="22"
        cy="22"
        r="16"
        className={frequent && on ? 'stroke-accent/60' : 'stroke-line'}
        strokeWidth="4"
        strokeDasharray={frequent ? '2 2.19' : undefined}
      />
      {degs.map((d) => (
        <circle key={d} cx="22" cy="6" r="5" className={cn('stroke-bg', on ? 'fill-accent' : 'fill-input')} strokeWidth="2" transform={`rotate(${d} 22 22)`} />
      ))}
    </svg>
  )
}
