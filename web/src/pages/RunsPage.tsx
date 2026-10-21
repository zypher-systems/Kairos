import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { ChevronRight, History as HistoryIcon, Search, TextSearch } from 'lucide-react'
import { useRuns, useJobs } from '@/hooks/queries'
import { useWS } from '@/hooks/useWS'
import { api } from '@/lib/api'
import type { LogSearchHit } from '@/lib/types'
import { StatusBadge, Input, EmptyState, QueryError, Button, Select } from '@/components/ui'
import { cn, fmtDateTime, fmtDuration, shortID, targetLabel } from '@/lib/utils'

const statusFilters: [string, string][] = [
  ['all', 'All'],
  ['failed', 'Failed'],
  ['running', 'Running'],
  ['queued', 'Queued'],
  ['pending_approval', 'Awaiting approval'],
  ['succeeded', 'Succeeded'],
]

const triggerLabel: Record<string, string> = {
  schedule: 'Schedule',
  manual: 'Manual',
  retry: 'Retry',
  workflow: 'Workflow',
  file: 'File trigger',
}

const PAGE = 50

export default function RunsPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const status = statusFilters.some(([v]) => v === searchParams.get('status')) ? searchParams.get('status')! : 'all'
  const jobFilter = searchParams.get('job') ?? ''
  const offset = Math.max(0, Number(searchParams.get('offset') ?? 0) || 0)
  const setParam = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(searchParams)
    for (const [k, v] of Object.entries(patch)) {
      if (!v || v === 'all' || v === '0') next.delete(k)
      else next.set(k, v)
    }
    setSearchParams(next, { replace: true })
  }
  const [q, setQ] = useState('')
  const qc = useQueryClient()
  const params: Record<string, string> = { limit: String(PAGE), offset: String(offset) }
  if (status !== 'all') params.status = status
  if (jobFilter) params.job_id = jobFilter
  const { data: runs, error, isLoading, isFetching } = useRuns(params)
  const { data: jobs } = useJobs()

  useWS('activity', () => {
    qc.invalidateQueries({ queryKey: ['runs'] })
    qc.invalidateQueries({ queryKey: ['stats'] })
  })

  const jobName = useMemo(() => {
    const m = new Map<string, string>()
    for (const j of jobs ?? []) m.set(j.id, j.name)
    return (id: string) => m.get(id) ?? shortID(id)
  }, [jobs])

  const needle = q.trim().toLowerCase()
  const filtered = (runs ?? []).filter((r) => needle === '' || `${jobName(r.job_id)} ${r.id}`.toLowerCase().includes(needle))
  const unfiltered = status === 'all' && !jobFilter && !needle

  return (
    <div className="anim-in space-y-6">
      <header>
        <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Runs</h1>
        <p className="mt-1.5 text-sm text-faint">Every run of every job, updated live.</p>
      </header>

      {error && <QueryError error={error} />}

      <div className="flex flex-wrap items-center gap-3">
        <div role="group" aria-label="Status" className="flex flex-wrap gap-1.5">
          {statusFilters.map(([v, label]) => (
            <button
              key={v}
              type="button"
              aria-pressed={status === v}
              onClick={() => setParam({ status: v, offset: null })}
              className={cn(
                'h-[34px] rounded-full border px-3.5 text-[13px] font-semibold transition-colors cursor-pointer',
                status === v
                  ? 'border-ink bg-ink text-bg'
                  : 'border-input/60 bg-panel text-ink hover:bg-raised',
              )}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="flex-1" />
        <Select aria-label="Job" wrapperClassName="w-full sm:w-52" value={jobFilter} onChange={(e) => setParam({ job: e.target.value, offset: null })}>
          <option value="">All jobs</option>
          {(jobs ?? []).map((j) => (
            <option key={j.id} value={j.id}>
              {j.name}
            </option>
          ))}
        </Select>
        <div className="relative w-full sm:w-60">
          <Search size={15} aria-hidden="true" className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-faint" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} aria-label="Filter this page" placeholder="Job name or run ID" className="pl-9" />
        </div>
      </div>

      <LogSearch jobs={jobs ?? []} />

      <div className="overflow-x-auto rounded-2xl border border-line bg-panel">
        <table className="w-full min-w-[760px] text-sm">
          <thead>
            <tr className="border-b border-line-soft text-left text-xs text-faint">
              <th className="px-5 py-3 font-semibold">Status</th>
              <th className="px-3 py-3 font-semibold">Job</th>
              <th className="px-3 py-3 font-semibold">Trigger</th>
              <th className="px-3 py-3 font-semibold">Target</th>
              <th className="px-3 py-3 font-semibold">Started</th>
              <th className="px-3 py-3 text-right font-semibold">Duration</th>
              <th className="w-10 px-3 py-3">
                <span className="sr-only">Open</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {isLoading &&
              Array.from({ length: 6 }, (_, i) => (
                <tr key={i} className="border-b border-line-soft last:border-b-0">
                  <td colSpan={7} className="px-5 py-3.5">
                    <div className="h-4 animate-pulse rounded bg-raised" style={{ width: `${60 + ((i * 13) % 35)}%` }} />
                  </td>
                </tr>
              ))}
            {filtered.map((r) => (
              <tr key={r.id} onClick={() => navigate(`/runs/${r.id}`)} className="cursor-pointer border-b border-line-soft last:border-b-0 hover:bg-raised/50">
                <td className="px-5 py-3">
                  <StatusBadge status={r.status} />
                </td>
                <td className="px-3 py-3">
                  <Link to={`/runs/${r.id}`} onClick={(e) => e.stopPropagation()} className="text-[15px] font-semibold text-ink hover:underline">
                    {jobName(r.job_id)}
                  </Link>
                  <div className="font-mono text-xs text-faint">
                    #{shortID(r.id)}
                    {r.attempt > 1 ? ` · attempt ${r.attempt}` : ''}
                  </div>
                </td>
                <td className="px-3 py-3 text-dim">{triggerLabel[r.trigger] ?? r.trigger}</td>
                <td className="px-3 py-3 text-dim">{targetLabel(r.target_type)}</td>
                <td className="px-3 py-3 text-sm tabular-nums whitespace-nowrap text-dim">{fmtDateTime(r.started_at ?? r.queued_at)}</td>
                <td className="px-3 py-3 text-right text-sm tabular-nums text-dim">{r.started_at ? fmtDuration(r.started_at, r.finished_at) : '—'}</td>
                <td className="px-3 py-3 text-faint">
                  <ChevronRight size={16} aria-hidden="true" />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!isLoading && filtered.length === 0 && (
          <EmptyState
            icon={HistoryIcon}
            title={unfiltered ? 'No runs yet' : 'No runs match'}
            sub={unfiltered ? 'Runs appear here when a job fires or you run one by hand.' : 'Try another status or job.'}
          />
        )}
      </div>

      <div className="flex items-center justify-between gap-3 text-[13px] text-faint">
        <span>
          {filtered.length > 0 && `Showing ${offset + 1}–${offset + (runs?.length ?? 0)}`}
          {isFetching && !isLoading && ' · updating…'}
        </span>
        <div className="flex gap-2">
          <Button size="sm" variant="secondary" disabled={offset === 0} onClick={() => setParam({ offset: String(Math.max(0, offset - PAGE)) })}>
            Newer
          </Button>
          <Button size="sm" variant="secondary" disabled={(runs?.length ?? 0) < PAGE} onClick={() => setParam({ offset: String(offset + PAGE) })}>
            Older
          </Button>
        </div>
      </div>
    </div>
  )
}

function LogSearch({ jobs }: { jobs: { id: string; name: string }[] }) {
  const [open, setOpen] = useState(false)
  const [logQ, setLogQ] = useState('')
  const [logJob, setLogJob] = useState('')
  const [hits, setHits] = useState<LogSearchHit[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)

  const search = () => {
    const needle = logQ.trim()
    if (needle.length < 2) return
    setBusy(true)
    setErr(null)
    const p: Record<string, string> = { q: needle }
    if (logJob) p.job_id = logJob
    api
      .searchLogs(p)
      .then((r) => setHits(r.hits ?? []))
      .catch((e) => setErr(e))
      .finally(() => setBusy(false))
  }

  if (!open) {
    return (
      <Button variant="secondary" onClick={() => setOpen(true)}>
        <TextSearch size={16} /> Search logs
      </Button>
    )
  }

  return (
    <section aria-label="Log search" className="space-y-3 rounded-2xl border border-line bg-panel p-5">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-52 flex-1">
          <label htmlFor="log-q" className="mb-1.5 block text-[13px] font-semibold text-dim">
            Text in the output
          </label>
          <Input
            id="log-q"
            autoFocus
            value={logQ}
            onChange={(e) => setLogQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && search()}
            className="font-mono"
            placeholder="connection refused"
          />
        </div>
        <Select aria-label="Job to search" value={logJob} onChange={(e) => setLogJob(e.target.value)} wrapperClassName="w-52">
          <option value="">All jobs</option>
          {jobs.map((j) => (
            <option key={j.id} value={j.id}>
              {j.name}
            </option>
          ))}
        </Select>
        <Button variant="primary" disabled={busy || logQ.trim().length < 2} onClick={search}>
          {busy ? 'Searching…' : 'Search'}
        </Button>
        <Button variant="ghost" onClick={() => setOpen(false)}>
          Close
        </Button>
      </div>
      {err != null && <QueryError error={err} />}
      {hits && hits.length === 0 && <p className="text-sm text-faint">No output matches.</p>}
      {hits && hits.length > 0 && (
        <ul className="divide-y divide-line-soft">
          {hits.map((h) => (
            <li key={`${h.run_id}-${h.seq}`}>
              <Link to={`/runs/${h.run_id}`} className="block py-2.5 hover:bg-raised/50">
                <span className="text-sm font-semibold text-ink">{h.job_name}</span>
                <span className="ml-2 font-mono text-xs text-faint">#{shortID(h.run_id)}</span>
                <span className="mt-0.5 block truncate font-mono text-[12.5px] text-dim">{h.snippet}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
