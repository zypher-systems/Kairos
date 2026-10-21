import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { AlertTriangle, ChevronRight, Download, Pause, Play, RotateCcw, Search, WrapText } from 'lucide-react'
import { useRun, useJobs } from '@/hooks/queries'
import { useWS } from '@/hooks/useWS'
import { api } from '@/lib/api'
import { Button, Input, StatusBadge, statusColor } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { cn, fmtDateTime, fmtDuration, shortID, targetLabel } from '@/lib/utils'
import type { LogChunk } from '@/lib/types'
import { WriteGate } from '@/lib/auth'

const PAGE = 1000
const MAX_PAGES = 100 // 100k chunks is far beyond any sane job log
const RENDER_WINDOW = 3000

const streamStyles: Record<string, string> = {
  stdout: 'text-terminal-ink',
  stderr: 'text-terminal-err',
  system: 'text-terminal-sys',
}

const triggerLabel: Record<string, string> = {
  schedule: 'Schedule',
  manual: 'Manual',
  retry: 'Retry',
  workflow: 'Workflow',
  file: 'File trigger',
}

type Line = { seq: number; stream: string; text: string }

function toLines(chunks: LogChunk[]): Line[] {
  const out: Line[] = []
  for (const c of [...chunks].sort((a, b) => a.seq - b.seq)) {
    const parts = c.data.split('\n')
    parts.forEach((p, i) => {
      if (i === parts.length - 1 && p === '') return
      out.push({ seq: c.seq, stream: c.stream, text: p })
    })
  }
  return out
}

export default function RunDetail() {
  const confirm = useConfirm()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { id } = useParams()
  const { data: run, refetch } = useRun(id)
  const { data: jobs } = useJobs()
  const job = (jobs ?? []).find((j) => j.id === run?.job_id)
  const [chunks, setChunks] = useState<LogChunk[]>([])
  const [loaded, setLoaded] = useState(false)
  const [truncated, setTruncated] = useState(false)
  const [follow, setFollow] = useState(true)
  const [wrap, setWrap] = useState(true)
  const [filter, setFilter] = useState('')
  const [showAll, setShowAll] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Load the whole backlog page by page (the API returns at most 1000 chunks).
  useEffect(() => {
    if (!id) return
    let alive = true
    setChunks([])
    setLoaded(false)
    ;(async () => {
      let after = '0'
      const all: LogChunk[] = []
      for (let page = 0; page < MAX_PAGES; page++) {
        const r = await api.getLogs(id, after, String(PAGE))
        all.push(...r.chunks)
        if (r.chunks.length < PAGE) break
        after = String(r.next)
        if (page === MAX_PAGES - 1) setTruncated(true)
      }
      if (!alive) return
      setChunks((live) => {
        const seen = new Set(all.map((c) => c.seq))
        return [...all, ...live.filter((c) => !seen.has(c.seq))]
      })
      setLoaded(true)
    })().catch(() => alive && setLoaded(true))
    return () => {
      alive = false
    }
  }, [id])

  useWS(`run:${id}`, (msg) => {
    if (msg.event !== 'log') return
    const c = msg.data as LogChunk
    setChunks((prev) => (prev.some((x) => x.seq === c.seq) ? prev : [...prev, c]))
  })

  useWS('activity', (msg) => {
    const e = msg.data as { id?: string }
    if (e.id === id) refetch()
  })

  const ended = !!run && ['succeeded', 'failed', 'canceled', 'skipped'].includes(run.status)
  const pending = run?.status === 'pending_approval'
  useEffect(() => {
    if (ended) setFollow(false)
  }, [ended])

  const lines = useMemo(() => toLines(chunks), [chunks])
  const needle = filter.trim().toLowerCase()
  const matching = needle ? lines.filter((l) => l.text.toLowerCase().includes(needle)) : lines
  const visible = showAll || matching.length <= RENDER_WINDOW ? matching : matching.slice(-RENDER_WINDOW)
  const hidden = matching.length - visible.length

  useEffect(() => {
    if (follow && scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight
  }, [visible.length, follow])

  // A finished run opens at the end of its output, where failures usually are.
  useEffect(() => {
    if (loaded && scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight
  }, [loaded])

  const download = () => {
    if (lines.length === 0) return toast.error('No output to download yet')
    const body = lines.map((l) => (l.stream === 'stdout' ? l.text : `[${l.stream}] ${l.text}`)).join('\n') + '\n'
    const url = URL.createObjectURL(new Blob([body], { type: 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${(job?.name ?? 'run').replace(/\s+/g, '-').toLowerCase()}-${(id ?? '').slice(0, 8)}.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  const act = (p: Promise<unknown>, msg: string) =>
    p
      .then(() => {
        toast.success(msg)
        refetch()
        qc.invalidateQueries({ queryKey: ['runs'] })
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))

  const rerun = () => {
    if (!run) return
    api
      .runJob(run.job_id)
      .then((r) => {
        toast.success('New run queued')
        navigate(`/runs/${r.id}`)
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
  }

  const sc = statusColor(run?.status ?? 'queued')
  const meta: [string, string][] = run
    ? [
        ['Trigger', triggerLabel[run.trigger] ?? run.trigger],
        ['Target', targetLabel(run.target_type)],
        ['Queued', fmtDateTime(run.queued_at)],
        ['Started', run.started_at ? fmtDateTime(run.started_at) : '—'],
        ['Duration', run.started_at ? fmtDuration(run.started_at, run.finished_at) : '—'],
        ['Exit code', run.exit_code != null ? String(run.exit_code) : '—'],
      ]
    : []

  return (
    <div className="anim-in flex h-full flex-col gap-5 pb-2">
      <div>
        <nav aria-label="Breadcrumb" className="mb-2 flex items-center gap-1.5 text-[13px] text-faint">
          <Link to="/runs" className="font-semibold hover:text-ink">
            Runs
          </Link>
          <ChevronRight size={14} aria-hidden="true" />
          <span className="font-mono">#{shortID(id ?? '')}</span>
        </nav>
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="min-w-0 truncate font-display text-[24px] leading-tight font-semibold tracking-[-0.03em] md:text-[28px] text-ink">{job?.name ?? 'Run'}</h1>
          {run && <StatusBadge status={run.status} />}
          {run && run.attempt > 1 && <span className="text-[13px] text-faint">attempt {run.attempt}</span>}
          <div className="flex-1" />
          {job && (
            <Link
              to={`/jobs/${job.id}`}
              className="inline-flex h-10 items-center rounded-md border border-input/60 bg-panel px-4 text-sm font-semibold text-ink hover:bg-raised"
            >
              Open job
            </Link>
          )}
          <WriteGate>
            {run && pending && (
              <>
                <Button className="bg-ink text-bg hover:bg-ink/85" onClick={() => act(api.approveRun(run.id), 'Run approved')}>
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
                    }).then((ok) => void (ok && act(api.rejectRun(run.id), 'Run rejected')))
                  }
                >
                  Reject…
                </Button>
              </>
            )}
            {run && !ended && !pending && (
              <Button
                variant="danger-ghost"
                onClick={() =>
                  confirm({
                    title: 'Cancel this run?',
                    body: 'The process is stopped. Output so far is kept.',
                    confirmLabel: 'Cancel run',
                    cancelLabel: 'Keep running',
                    destructive: true,
                  }).then(
                    (ok) =>
                      void (ok &&
                        act(
                          fetch(`/api/v1/runs/${run.id}/cancel`, { method: 'POST' }).then((r) => {
                            if (!r.ok) throw new Error('Cancel failed')
                          }),
                          'Cancellation requested',
                        )),
                  )
                }
              >
                Cancel run…
              </Button>
            )}
            {run && ended && (
              <Button variant="primary" onClick={rerun}>
                <RotateCcw size={15} /> Run again
              </Button>
            )}
          </WriteGate>
        </div>
      </div>

      {run && (
        <dl className="grid grid-cols-2 gap-x-6 gap-y-3 rounded-2xl border border-line bg-panel px-5 py-4 sm:grid-cols-3 xl:grid-cols-6">
          {meta.map(([k, v]) => (
            <div key={k} className="min-w-0">
              <dt className="text-xs font-semibold text-faint">{k}</dt>
              <dd className={cn('mt-0.5 text-sm break-words text-ink tabular-nums', k === 'Exit code' && run.exit_code && sc.text)}>{v}</dd>
            </div>
          ))}
        </dl>
      )}

      {run?.status === 'failed' && run.error && (
        <div role="alert" className="flex items-start gap-3 rounded-2xl bg-danger-soft px-5 py-3.5 text-sm text-danger">
          <AlertTriangle size={18} className="mt-0.5 shrink-0" aria-hidden="true" />
          <span className="font-mono text-[13px]">{run.error}</span>
        </div>
      )}

      <section aria-label="Output" className="flex min-h-[420px] flex-1 flex-col overflow-hidden rounded-2xl border border-line">
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line bg-panel px-4 py-2.5">
          <h2 className="mr-2 text-sm font-semibold text-ink">Output</h2>
          <span className="flex items-center gap-3 text-xs text-faint" aria-hidden="true">
            <span className="flex items-center gap-1">
              <span className="h-2 w-2 rounded-full bg-terminal-ink ring-1 ring-line" /> stdout
            </span>
            <span className="flex items-center gap-1">
              <span className="h-2 w-2 rounded-[1px] bg-terminal-err" /> stderr
            </span>
            <span className="flex items-center gap-1">
              <span className="h-2 w-2 rounded-full bg-terminal-sys" /> Kairos
            </span>
          </span>
          <div className="flex-1" />
          <div className="relative w-full sm:w-56">
            <Search size={14} aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-faint" />
            <Input value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Show only lines containing" placeholder="Filter lines" className="h-8 pl-8 text-[13px]" />
          </div>
          {needle && <span className="text-xs text-faint">{matching.length} matching</span>}
          <Button size="sm" variant="ghost" aria-pressed={wrap} onClick={() => setWrap((w) => !w)}>
            <WrapText size={14} /> Wrap
          </Button>
          {!ended && (
            <Button size="sm" variant="ghost" aria-pressed={follow} onClick={() => setFollow((f) => !f)}>
              {follow ? <Pause size={14} /> : <Play size={14} />} {follow ? 'Following' : 'Paused'}
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={download}>
            <Download size={14} /> Download
          </Button>
        </div>
        <div
          ref={scrollRef}
          role="log"
          aria-live={follow && !ended ? 'polite' : 'off'}
          className={cn(
            'term-scroll min-h-0 flex-1 overflow-auto bg-terminal px-4 py-3 font-mono text-[13px] leading-[1.6] text-terminal-ink',
            'max-h-[70vh]',
          )}
        >
          {!loaded && <div className="text-terminal-dim">Loading output…</div>}
          {loaded && lines.length === 0 && (
            <div className="text-terminal-dim italic">
              No output yet
              {run?.status === 'queued' ? ': waiting for a worker to pick it up.' : ''}
              {pending ? ': waiting for approval.' : ''}
            </div>
          )}
          {truncated && <div className="mb-2 text-xs text-terminal-warn">Only the first {MAX_PAGES * PAGE} log chunks were loaded.</div>}
          {hidden > 0 && (
            <button type="button" onClick={() => setShowAll(true)} className="mb-2 text-xs text-terminal-sys underline-offset-2 hover:underline">
              {hidden} earlier lines hidden. Show all
            </button>
          )}
          {visible.map((l, i) => (
            <div key={`${l.seq}-${i}`} className={cn(wrap ? 'break-words whitespace-pre-wrap' : 'whitespace-pre', streamStyles[l.stream])}>
              {l.text || ' '}
            </div>
          ))}
          {!ended && run && loaded && (
            <span className="pulse-dot text-terminal-sys" aria-hidden="true">
              ▋
            </span>
          )}
        </div>
      </section>
    </div>
  )
}
