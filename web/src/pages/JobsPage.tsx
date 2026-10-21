import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import * as DM from '@radix-ui/react-dropdown-menu'
import { AlertTriangle, CalendarClock, FileSearch, MoreHorizontal, Play, Plus, Search, ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'
import {
  useAgents,
  useCreateJob,
  useDeleteJob,
  useJobs,
  useLatestRuns,
  useRunJob,
  useRuns,
  useSSHHosts,
  useUpdateJob,
} from '@/hooks/queries'
import { Button, Switch, EmptyState, Input, Dialog, QueryError, Textarea, Field, StatusDot, statusColor } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { api, type ApplyResult } from '@/lib/api'
import { describeSchedule, relFuture, clock, scheduleZone } from '@/lib/schedule'
import { cn } from '@/lib/utils'
import { MiniDial } from '@/components/DayDial'
import type { AgentInfo, Job, Run, SSHHost } from '@/lib/types'
import { WriteGate, useAuth } from '@/lib/auth'

type GroupBy = 'next' | 'target' | 'tag'

function targetLabel(j: Job, agents?: AgentInfo[], hosts?: SSHHost[]): string {
  switch (j.target_type) {
    case 'local':
      return 'Kairos server'
    case 'agent': {
      const id = j.target_selector?.agent_id
      return agents?.find((a) => a.id === id)?.name ?? 'Agent'
    }
    case 'ssh':
      return hosts?.find((h) => h.id === j.ssh_host_id)?.name ?? 'SSH host'
    case 'selector': {
      const labels = Object.entries(j.target_selector ?? {}).map(([k, v]) => `${k}=${v}`)
      return labels.length ? labels.join(', ') : 'Selector'
    }
  }
  return j.target_type
}

const modeLabel: Record<string, string> = { shell: 'shell', docker: 'docker', transfer: 'file transfer' }

function byNextRun(a: Job, b: Job) {
  if (a.enabled !== b.enabled) return a.enabled ? -1 : 1
  const ta = a.next_run_at ? Date.parse(a.next_run_at) : Infinity
  const tb = b.next_run_at ? Date.parse(b.next_run_at) : Infinity
  return ta - tb || a.name.localeCompare(b.name)
}

export default function JobsPage() {
  const { data: jobs, isLoading, error } = useJobs()
  const { data: agents } = useAgents()
  const { data: hosts } = useSSHHosts()
  const { data: latest } = useLatestRuns()
  const { data: approvals } = useRuns({ status: 'pending_approval', limit: '50' })
  const [q, setQ] = useState('')
  const [group, setGroup] = useState<GroupBy>('next')

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (jobs ?? [])
      .filter(
        (j) =>
          needle === '' ||
          j.name.toLowerCase().includes(needle) ||
          j.description.toLowerCase().includes(needle) ||
          j.tags.some((t) => t.toLowerCase().includes(needle)),
      )
      .sort(byNextRun)
  }, [jobs, q])

  const groups = useMemo(() => {
    if (group === 'next') return [{ label: '', jobs: filtered }]
    const map = new Map<string, Job[]>()
    for (const j of filtered) {
      const keys = group === 'tag' ? (j.tags.length ? j.tags : ['Untagged']) : [targetLabel(j, agents, hosts)]
      for (const k of keys) map.set(k, [...(map.get(k) ?? []), j])
    }
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([label, jobs]) => ({ label, jobs }))
  }, [filtered, group, agents, hosts])

  const nameOf = (id: string) => jobs?.find((j) => j.id === id)?.name ?? 'a job'
  const failing = latest ? [...latest.byJob.values()].filter((r) => r.status === 'failed') : []
  const waiting = approvals ?? []
  const hostCount = (agents?.length ?? 0) + (hosts?.length ?? 0)

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Jobs</h1>
          <p className="mt-1.5 text-sm text-faint">
            {jobs?.length ?? 0} {jobs?.length === 1 ? 'job' : 'jobs'}
            {hostCount > 0 && ` across ${hostCount} ${hostCount === 1 ? 'host' : 'hosts'}`}
          </p>
        </div>
        <ExportButton />
        <WriteGate>
          <ApplyDialog />
          <Link
            to="/jobs/new"
            className="inline-flex h-10 items-center gap-2 rounded-md bg-accent px-4 text-sm font-semibold text-accent-fg hover:bg-accent/90"
          >
            <Plus size={16} strokeWidth={2.4} aria-hidden="true" />
            New job
          </Link>
        </WriteGate>
      </header>

      {error && <QueryError error={error} />}

      {(failing.length > 0 || waiting.length > 0) && (
        <section aria-label="Needs you" className="grid gap-4 lg:grid-cols-2">
          {failing.slice(0, 2).map((r) => (
            <div key={r.id} className="flex items-center gap-4 rounded-2xl bg-danger-soft px-5 py-4">
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-danger text-danger-fg">
                <AlertTriangle size={19} aria-hidden="true" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="font-semibold text-danger">
                  <span>{nameOf(r.job_id)}</span> is failing
                </div>
                <div className="truncate text-[13px] text-dim">
                  Last run {clock(r.finished_at ?? r.created_at)}
                  {r.error ? ` · ${r.error}` : ''}
                </div>
              </div>
              <Link
                to={`/runs/${r.id}`}
                className="inline-flex h-9 shrink-0 items-center rounded-full border border-input/60 bg-panel px-3.5 text-[13px] font-semibold text-ink hover:bg-raised"
              >
                Open log
              </Link>
            </div>
          ))}
          {waiting.length > 0 && (
            <div className="flex items-center gap-4 rounded-2xl bg-raised px-5 py-4">
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-warn-soft text-warn">
                <ShieldCheck size={19} aria-hidden="true" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="font-semibold text-ink">
                  {waiting.length === 1 ? (
                    <>
                      <span>{nameOf(waiting[0].job_id)}</span> needs approval
                    </>
                  ) : (
                    `${waiting.length} runs need approval`
                  )}
                </div>
                <div className="text-[13px] text-faint">Waiting since {clock(waiting[waiting.length - 1].queued_at)}</div>
              </div>
              <Link
                to={waiting.length === 1 ? `/runs/${waiting[0].id}` : '/runs?status=pending_approval'}
                className="inline-flex h-9 shrink-0 items-center rounded-full bg-ink px-4 text-[13px] font-semibold text-bg hover:bg-ink/85"
              >
                Review
              </Link>
            </div>
          )}
        </section>
      )}

      {(jobs ?? []).length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          <div role="group" aria-label="Group jobs" className="flex gap-1.5">
            {(
              [
                ['next', 'By next run'],
                ['target', 'By host'],
                ['tag', 'By tag'],
              ] as [GroupBy, string][]
            ).map(([v, label]) => (
              <button
                key={v}
                type="button"
                aria-pressed={group === v}
                onClick={() => setGroup(v)}
                className={cn(
                  'h-[34px] rounded-full border px-3.5 text-[13px] font-semibold transition-colors cursor-pointer',
                  group === v ? 'border-ink bg-ink text-bg' : 'border-input/60 bg-panel text-ink hover:bg-raised',
                )}
              >
                {label}
              </button>
            ))}
          </div>
          <div className="flex-1" />
          <div className="relative w-full sm:w-72">
            <Search size={15} aria-hidden="true" className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-faint" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} aria-label="Filter jobs" placeholder="Filter by name, description or tag" className="pl-9" />
          </div>
        </div>
      )}

      {isLoading && <div className="py-12 text-center text-sm text-faint">Loading…</div>}
      {!isLoading && (jobs ?? []).length === 0 && (
        <div className="rounded-2xl border border-line bg-panel">
          <EmptyState
            icon={CalendarClock}
            title="No jobs yet"
            sub="A job is a script or container that runs on a schedule, on one of your hosts or on the Kairos server itself."
            action={
              <WriteGate>
                <Link
                  to="/jobs/new"
                  className="inline-flex h-10 items-center gap-2 rounded-md bg-accent px-4 text-sm font-semibold text-accent-fg hover:bg-accent/90"
                >
                  <Plus size={16} aria-hidden="true" /> Create your first job
                </Link>
              </WriteGate>
            }
          />
        </div>
      )}
      {!isLoading && (jobs ?? []).length > 0 && filtered.length === 0 && (
        <div className="rounded-2xl border border-line bg-panel">
          <EmptyState icon={Search} title="No jobs match" sub="Try a different name or tag." />
        </div>
      )}

      {groups.map((g) =>
        g.jobs.length === 0 ? null : (
          <section key={g.label || 'all'} aria-label={g.label || 'All jobs'}>
            {g.label && <h2 className="mb-2 px-1 text-[13px] font-semibold text-faint">{g.label}</h2>}
            <ul className="rounded-2xl border border-line bg-panel px-3 md:px-5">
              {g.jobs.map((j) => (
                <JobRow key={j.id} job={j} target={targetLabel(j, agents, hosts)} last={latest?.byJob.get(j.id)} />
              ))}
            </ul>
          </section>
        ),
      )}

    </div>
  )
}

function JobRow({ job: j, target, last }: { job: Job; target: string; last?: Run }) {
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { canWrite } = useAuth()
  const runNow = useRunJob()
  const upd = useUpdateJob()
  const del = useDeleteJob()
  const createJob = useCreateJob()
  const sched = describeSchedule(j)
  const sc = last ? statusColor(last.status) : null

  const clone = () => {
    const { id: _id, created_at: _c, updated_at: _u, next_run_at: _n, last_run_at: _l, ...rest } = j
    createJob.mutate(
      { ...rest, name: `${j.name} copy` },
      {
        onSuccess: (created) => {
          toast.success(`Duplicated as "${created.name}"`)
          navigate(`/jobs/${created.id}`)
        },
        onError: (e) => toast.error(String(e)),
      },
    )
  }
  const remove = async () => {
    const ok = await confirm({
      title: `Delete ${j.name}?`,
      body: 'The job, its runs and their logs are deleted. This cannot be undone.',
      confirmLabel: 'Delete job',
      destructive: true,
    })
    if (!ok) return
    del.mutate(j.id, {
      onSuccess: () => toast.success(`${j.name} deleted`),
      onError: (e) => toast.error(String(e)),
    })
  }

  const menuItem = 'flex h-9 cursor-pointer items-center rounded-md px-2.5 text-sm outline-none select-none data-[highlighted]:bg-raised'

  return (
    <li
      className={cn(
        'grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 border-b border-line-soft py-3.5 last:border-b-0',
        'md:grid-cols-[190px_minmax(0,1fr)_120px_170px_auto]',
      )}
    >
      <div className="hidden items-center gap-3.5 md:flex" title={sched.sentence}>
        <MiniDial job={j} />
        <div className="min-w-0">
          <div className={cn('truncate font-display text-[17px] leading-tight font-semibold tracking-[-0.03em] tabular-nums', j.enabled ? 'text-ink' : 'text-faint')}>
            {sched.big}
          </div>
          <div className="truncate text-[13px] text-faint">{sched.small}</div>
        </div>
      </div>

      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <Link to={`/jobs/${j.id}`} className={cn('truncate text-[15px] font-semibold hover:underline', j.enabled ? 'text-ink' : 'text-faint')}>
            {j.name}
          </Link>
          {j.tags.slice(0, 3).map((t) => (
            <span key={t} className="rounded-full bg-raised px-2.5 py-px text-xs font-medium text-dim">
              {t}
            </span>
          ))}
        </div>
        <div className="mt-0.5 truncate text-[12.5px] text-faint">
          <span className="md:hidden">{sched.sentence} · </span>
          {target} · {modeLabel[j.exec_mode] ?? j.exec_mode}
          {j.require_approval && ' · approval required'}
          {j.watch_enabled && (
            <span className="ml-1 inline-flex items-center gap-1">
              · <FileSearch size={11} aria-hidden="true" /> watches {j.watch_path}
            </span>
          )}
        </div>
      </div>

      <div className="hidden md:block">
        <div className="text-xs text-faint">Next</div>
        <div className={cn('text-sm font-semibold', j.enabled ? 'text-ink' : 'text-faint')}>
          {j.enabled ? relFuture(j.next_run_at, Date.now(), scheduleZone(j)) : 'Paused'}
        </div>
      </div>

      <div className="hidden md:block">
        {sc ? (
          <Link to={`/runs/${last!.id}`} className={cn('inline-flex min-h-9 items-center gap-2 text-sm font-semibold hover:underline', sc.text)}>
            <StatusDot status={last!.status} />
            {sc.label} {clock(last!.finished_at ?? last!.created_at)}
          </Link>
        ) : (
          <span className="text-[13px] text-faint">Never run</span>
        )}
      </div>

      <div className="flex items-center justify-end gap-2">
        <Switch
          checked={j.enabled}
          disabled={!canWrite}
          label={`Enable ${j.name}`}
          onChange={(v) =>
            upd.mutate(
              { id: j.id, job: { ...j, enabled: v } },
              { onSuccess: () => toast.success(v ? `${j.name} enabled` : `${j.name} paused`) },
            )
          }
        />
        <WriteGate>
          <Button
            size="sm"
            variant="secondary"
            aria-label={`Run ${j.name} now`}
            onClick={() => runNow.mutate(j.id, { onSuccess: () => toast.success(`${j.name} queued`), onError: (e) => toast.error(String(e)) })}
          >
            <Play size={12} fill="currentColor" aria-hidden="true" />
            Run
          </Button>
          <DM.Root>
            <DM.Trigger asChild>
              <Button variant="ghost" size="icon" aria-label={`Actions for ${j.name}`} className="h-8 w-8">
                <MoreHorizontal size={17} />
              </Button>
            </DM.Trigger>
            <DM.Portal>
              <DM.Content align="end" sideOffset={6} className="anim-in z-50 w-44 rounded-xl border border-line bg-panel p-1.5 text-ink shadow-xl">
                <DM.Item className={menuItem} onSelect={() => navigate(`/jobs/${j.id}`)}>
                  Edit
                </DM.Item>
                <DM.Item className={menuItem} onSelect={clone}>
                  Duplicate
                </DM.Item>
                <DM.Separator className="my-1 h-px bg-line-soft" />
                <DM.Item className={cn(menuItem, 'text-danger')} onSelect={remove}>
                  Delete…
                </DM.Item>
              </DM.Content>
            </DM.Portal>
          </DM.Root>
        </WriteGate>
      </div>
    </li>
  )
}

function ExportButton() {
  const [busy, setBusy] = useState(false)
  const download = () => {
    setBusy(true)
    api
      .exportYAML()
      .then((text) => {
        const blob = new Blob([text], { type: 'application/yaml' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = 'kairos.yaml'
        a.click()
        URL.revokeObjectURL(url)
        toast.success('Exported jobs and workflows')
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setBusy(false))
  }
  return (
    <Button variant="secondary" disabled={busy} onClick={download}>
      {busy ? 'Exporting…' : 'Export YAML'}
    </Button>
  )
}

function ApplyDialog() {
  const [open, setOpen] = useState(false)
  const [yaml, setYaml] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<ApplyResult | null>(null)

  const run = (dry: boolean) => {
    setBusy(true)
    api
      .applyYAML(yaml, dry)
      .then(setResult)
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setBusy(false))
  }

  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        Apply YAML
      </Button>
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        title="Apply YAML"
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Close
            </Button>
            <Button variant="secondary" disabled={busy || !yaml.trim()} onClick={() => run(true)}>
              Dry run
            </Button>
            <Button variant="primary" disabled={busy || !yaml.trim()} onClick={() => run(false)}>
              Apply
            </Button>
          </>
        }
      >
        <Field label="Documents" hint="Job and Workflow documents. Each is created or updated by metadata.name. Dry run shows the changes first.">
          <Textarea
            value={yaml}
            onChange={(e) => setYaml(e.target.value)}
            className="min-h-48 font-mono text-[12px]"
            placeholder={'apiVersion: kairos/v1\nkind: Job\nmetadata:\n  name: example'}
          />
        </Field>
        {result && (
          <div className="mt-3 space-y-1">
            {result.items.map((it, i) => (
              <div key={`${it.kind}-${it.name}-${i}`} className="font-mono text-[12px] text-dim">
                {it.kind} {it.name}: {it.error ? it.error : it.action}
                {it.changes?.length ? ` (${it.changes.join(', ')})` : ''}
              </div>
            ))}
            {result.items.length === 0 && <div className="text-[13px] text-faint">No documents.</div>}
          </div>
        )}
      </Dialog>
    </>
  )
}
