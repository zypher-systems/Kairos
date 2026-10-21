import { useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Check, X } from 'lucide-react'
import { useAgents, useChannels, useJobs, useLatestRuns, useRuns, useSSHHosts, useStats } from '@/hooks/queries'
import { useNow } from '@/hooks/useNow'
import { useWS } from '@/hooks/useWS'
import { Button, LinkButton, StatusDot, statusColor } from '@/components/ui'
import { DayDial } from '@/components/DayDial'
import { useConfirm } from '@/components/confirm'
import { api } from '@/lib/api'
import { WriteGate } from '@/lib/auth'
import { clock } from '@/lib/schedule'
import { cn, fmtDuration, shortID, targetLabel } from '@/lib/utils'
import type { Job, Run } from '@/lib/types'

const SETUP_DISMISS_KEY = 'kairos-setup-dismissed'

export default function Dashboard() {
  const qc = useQueryClient()
  const now = useNow()
  const { data: stats } = useStats()
  const { data: jobs } = useJobs()
  const { data: latest } = useLatestRuns(200)
  const { data: failed } = useRuns({ status: 'failed', limit: '50' })
  const { data: approvals } = useRuns({ status: 'pending_approval', limit: '20' })
  const { data: agents } = useAgents()

  useWS('activity', () => {
    qc.invalidateQueries({ queryKey: ['stats'] })
    qc.invalidateQueries({ queryKey: ['runs'] })
  })

  const nameOf = (id: string) => jobs?.find((j) => j.id === id)?.name ?? shortID(id)
  const recent = latest?.runs.slice(0, 7) ?? []
  // The dial plots the latest runs plus every failure, so an early failure
  // still shows after a busy job has pushed it off the latest page.
  const dialRuns = useMemo(() => [...(latest?.runs ?? []), ...(failed ?? [])], [latest, failed])

  // Failed runs whose job has not succeeded since: what still needs a look.
  const failing = useMemo(() => {
    if (!latest) return []
    return [...latest.byJob.values()].filter((r) => r.status === 'failed').slice(0, 3)
  }, [latest])
  const offline = agents?.filter((a) => !a.online) ?? []
  const pending = approvals ?? []
  const attention = failing.length + pending.length + offline.length

  const headline =
    (jobs?.length ?? 0) === 0
      ? 'Welcome to Kairos.'
      : attention === 0
        ? 'Everything’s on time.'
        : attention === 1
          ? 'One thing needs you.'
          : `${attention} things need you.`

  const finished = (stats?.day_succeeded ?? 0) + (stats?.day_failed ?? 0)

  return (
    <div className="anim-in space-y-8">
      <SetupChecklist jobs={jobs} />

      <div className="grid items-center gap-x-12 gap-y-8 lg:grid-cols-[minmax(0,400px)_minmax(0,1fr)]">
        <DayDial jobs={jobs ?? []} runs={dialRuns} now={now} />

        <div className="flex min-w-0 flex-col gap-4">
          <header>
            <div className="text-sm text-faint">
              {now.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' })}, {clock(now.toISOString())}
            </div>
            <h1 className="mt-1 font-display text-[26px] leading-[1.15] font-semibold tracking-[-0.03em] text-balance text-ink md:text-[30px]">
              {headline}
            </h1>
          </header>

          <section aria-label="Needs you" className="flex flex-col gap-3">
            {attention === 0 && (jobs?.length ?? 0) > 0 && (
              <div className="flex items-center gap-3 rounded-xl bg-ok-soft px-[18px] py-4 text-sm font-medium text-ok">
                <Check size={18} aria-hidden="true" />
                Nothing is failing or waiting on you.
              </div>
            )}
            {(jobs?.length ?? 0) === 0 && jobs && (
              <NeedCard
                title="Create your first job"
                detail="A job is a script, a container or a file transfer that runs on a schedule."
                actions={<LinkButton to="/jobs/new" variant="primary">New job</LinkButton>}
              />
            )}
            {failing.map((r) => (
              <FailedCard key={r.id} run={r} name={nameOf(r.job_id)} />
            ))}
            {pending.map((r) => (
              <ApprovalCard key={r.id} run={r} name={nameOf(r.job_id)} />
            ))}
            {offline.map((a) => (
              <NeedCard
                key={a.id}
                mark={<span aria-hidden="true" className="h-8 w-8 shrink-0 rounded-full border-[3px] border-dashed border-input" />}
                title={`Agent ${a.name} is offline`}
                detail={a.last_heartbeat_at ? `Last heartbeat ${clock(a.last_heartbeat_at)}. Its jobs wait until it is back.` : 'It has never connected.'}
                actions={<LinkButton to="/targets">Open hosts</LinkButton>}
              />
            ))}
          </section>
        </div>
      </div>

      <div className="grid gap-x-12 gap-y-8 xl:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]">
        <section>
          <div className="flex items-center">
            <h2 className="flex-1 font-display text-[17px] font-semibold tracking-[-0.02em] text-ink">Recent runs</h2>
            <Link to="/runs" className="flex h-11 items-center text-sm font-semibold text-accent hover:underline">
              All runs
            </Link>
          </div>
          {recent.length === 0 ? (
            <p className="border-t border-line py-8 text-center text-sm text-faint">No runs yet. They appear here as soon as a job fires.</p>
          ) : (
            <ul>
              {recent.map((r) => (
                <RecentRow key={r.id} run={r} name={nameOf(r.job_id)} />
              ))}
            </ul>
          )}
        </section>

        <section aria-label="Last 24 hours" className="flex flex-col gap-3.5">
          <h2 className="flex h-11 items-center font-display text-[17px] font-semibold tracking-[-0.02em] text-ink">Last 24 hours</h2>
          <div className="grid grid-cols-2 gap-3">
            <Numeral value={finished} label="runs finished" />
            <Numeral value={stats?.day_failed ?? 0} label="failed" tone={(stats?.day_failed ?? 0) > 0 ? 'danger' : undefined} />
          </div>
          <p className="text-sm text-faint">
            {finished > 0 && <>{successRate(stats?.day_succeeded, stats?.day_failed)} succeeded. </>}
            {stats?.running ?? 0} running now, {stats?.queued ? `${stats.queued} queued` : 'none queued'}.
          </p>
        </section>
      </div>
    </div>
  )
}

function successRate(ok?: number, failed?: number): string {
  const total = (ok ?? 0) + (failed ?? 0)
  if (total === 0) return '—'
  const pct = ((ok ?? 0) / total) * 100
  // Never round a day with failures up to a clean 100%.
  return `${pct === 100 || pct < 99 ? Math.round(pct) : Math.min(99.9, Math.floor(pct * 10) / 10)}%`
}

function Numeral({ value, label, tone }: { value: number | string; label: string; tone?: 'danger' }) {
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-line bg-panel px-[18px] py-4">
      <div className={cn('font-display text-[26px] leading-tight font-semibold tracking-[-0.03em] tabular-nums', tone === 'danger' ? 'text-danger' : 'text-ink')}>
        {value}
      </div>
      <div className="text-sm text-faint">{label}</div>
    </div>
  )
}

function RecentRow({ run, name }: { run: Run; name: string }) {
  const sc = statusColor(run.status)
  const when = run.started_at ?? run.queued_at ?? run.created_at
  return (
    <li className="border-t border-line">
      <Link to={`/runs/${run.id}`} className="grid min-h-[52px] grid-cols-[44px_12px_minmax(0,1fr)_auto] items-center gap-3.5 hover:bg-raised/50">
        <span className="text-sm text-faint tabular-nums">{clock(when)}</span>
        <StatusDot status={run.status} />
        <span className="min-w-0 truncate">
          <span className="font-semibold text-ink">{name}</span>
          <span className="ml-3 text-sm text-faint">
            {targetLabel(run.target_type)}
            {run.finished_at && run.started_at ? `, ${fmtDuration(run.started_at, run.finished_at)}` : ''}
          </span>
        </span>
        <span className={cn('text-sm font-semibold', sc.text)}>{sc.label}</span>
      </Link>
    </li>
  )
}

// One thing that needs a person: a mark, what happened, and what to do.
function NeedCard({
  tone,
  mark,
  title,
  detail,
  actions,
}: {
  tone?: 'danger'
  mark?: ReactNode
  title: ReactNode
  detail?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className={cn('flex flex-wrap items-center gap-x-3.5 gap-y-3 rounded-xl px-[18px] py-4', tone === 'danger' ? 'bg-danger-soft' : 'border border-line bg-panel')}>
      {mark}
      <div className="min-w-0 flex-[1_1_220px]">
        <div className={cn('font-bold', tone === 'danger' ? 'text-danger' : 'text-ink')}>{title}</div>
        {detail && <div className="mt-0.5 text-sm break-words text-dim">{detail}</div>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  )
}

function FailedCard({ run, name }: { run: Run; name: string }) {
  const qc = useQueryClient()
  const why = run.error || (run.exit_code != null ? `exit code ${run.exit_code}` : '')
  return (
    <NeedCard
      tone="danger"
      mark={
        <span aria-hidden="true" className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[9px] bg-danger font-bold text-danger-fg">
          !
        </span>
      }
      title={`${name} failed at ${clock(run.finished_at ?? run.created_at)}`}
      detail={why || 'No error text.'}
      actions={
        <>
          <WriteGate>
            <Button
              variant="primary"
              onClick={() =>
                api
                  .runJob(run.job_id)
                  .then(() => {
                    toast.success(`${name} queued`)
                    qc.invalidateQueries({ queryKey: ['runs'] })
                  })
                  .catch((e) => toast.error(String(e)))
              }
            >
              Retry now
            </Button>
          </WriteGate>
          <LinkButton to={`/runs/${run.id}`}>Open log</LinkButton>
        </>
      }
    />
  )
}

function ApprovalCard({ run, name }: { run: Run; name: string }) {
  const qc = useQueryClient()
  const confirm = useConfirm()
  const done = (msg: string) => () => {
    toast.success(msg)
    qc.invalidateQueries({ queryKey: ['runs'] })
  }
  return (
    <NeedCard
      mark={<span aria-hidden="true" className="h-8 w-8 shrink-0 rounded-full border-[3px] border-warn" />}
      title={`${name} is waiting for approval`}
      detail={`Queued ${clock(run.queued_at ?? run.created_at)}`}
      actions={
        <WriteGate>
          <Button variant="primary" onClick={() => api.approveRun(run.id).then(done('Run approved')).catch((e) => toast.error(String(e)))}>
            Approve
          </Button>
          <Button
            variant="secondary"
            onClick={() =>
              confirm({
                title: 'Reject this run?',
                body: 'The run will not start. The job runs again at its next scheduled time.',
                confirmLabel: 'Reject run',
                destructive: true,
              }).then((ok) => void (ok && api.rejectRun(run.id).then(done('Run rejected')).catch((e) => toast.error(String(e)))))
            }
          >
            Reject…
          </Button>
        </WriteGate>
      }
    />
  )
}

// SetupChecklist guides a new install; it hides itself once dismissed or done.
function SetupChecklist({ jobs }: { jobs: Job[] | undefined }) {
  const { data: agents } = useAgents()
  const { data: hosts } = useSSHHosts()
  const { data: channels } = useChannels()
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(SETUP_DISMISS_KEY) === '1'
    } catch {
      return false
    }
  })
  if (dismissed || !jobs || !agents || !hosts || !channels) return null

  const steps = [
    { done: agents.length + hosts.length > 0, label: 'Connect a host', to: '/targets' },
    { done: jobs.length > 0, label: 'Create a job', to: '/jobs/new' },
    { done: channels.length > 0, label: 'Add an alert channel', to: '/notifications' },
  ]
  const doneCount = steps.filter((s) => s.done).length
  if (doneCount === steps.length) return null

  const dismiss = () => {
    setDismissed(true)
    try {
      localStorage.setItem(SETUP_DISMISS_KEY, '1')
    } catch {
      // not persisted
    }
  }
  // The ring is 12 units round; its arc is the share of steps done.
  const round = 2 * Math.PI * 12

  return (
    <section aria-label="Finish setting up" className="flex w-fit max-w-full flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border border-line bg-panel py-1.5 pr-1.5 pl-2">
      <svg width="32" height="32" viewBox="0 0 32 32" fill="none" aria-hidden="true" className="shrink-0">
        <circle cx="16" cy="16" r="12" className="stroke-line" strokeWidth="4" />
        <circle
          cx="16"
          cy="16"
          r="12"
          className="stroke-accent"
          strokeWidth="4"
          strokeLinecap="round"
          strokeDasharray={`${(doneCount / steps.length) * round} ${round}`}
          transform="rotate(-90 16 16)"
        />
      </svg>
      <span className="text-sm font-semibold text-ink">
        Setup, {doneCount} of {steps.length} done.
      </span>
      <ul className="flex flex-wrap gap-x-4 text-sm">
        {steps
          .filter((s) => !s.done)
          .map((s) => (
            <li key={s.label}>
              <Link to={s.to} className="font-semibold text-accent hover:underline">
                {s.label}
              </Link>
            </li>
          ))}
      </ul>
      <Button variant="ghost" size="icon" aria-label="Dismiss setup checklist" onClick={dismiss}>
        <X size={16} />
      </Button>
    </section>
  )
}
