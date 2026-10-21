import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { Cron } from 'croner'
import { ArrowLeftRight, ArrowRight, Box, ChevronRight, Plus, Terminal, Trash2, X } from 'lucide-react'
import { useJob, useCreateJob, useUpdateJob, useRunJob, useAgents, useSSHHosts, useChannels, useRuns, useStorageLocations } from '@/hooks/queries'
import { api } from '@/lib/api'
import { Button, Input, Textarea, Select, Field, Switch, Tabs, StatusBadge } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { TZ_LIST, type Job, type AgentInfo, type SSHHost, type FileEntry, type ScheduleType, type ExecMode, type TransferEndpoint, type EndpointKind, type StorageLocation, type SecretStore, type Calendar } from '@/lib/types'
import { describeSchedule, firingsBetween, scheduleZone } from '@/lib/schedule'
import { fmtDateTime, fmtDayTime, cn, timeAgo } from '@/lib/utils'
import { useAuth } from '@/lib/auth'

type Draft = Omit<Job, 'id' | 'created_at' | 'updated_at' | 'next_run_at' | 'last_run_at' | 'workflow_id'>

const emptyDraft: Draft = {
  name: '',
  description: '',
  schedule_type: 'cron',
  cron_expr: '0 2 * * *',
  interval_seconds: 3600,
  oneoff_at: null,
  timezone: 'UTC',
  target_type: 'local',
  target_selector: {},
  ssh_host_id: null,
  exec_mode: 'shell',
  docker_image: 'alpine:3.20',
  docker_command: [],
  shell_script: 'echo "hello from kairos"',
  workdir: '',
  transfer_config: {
    source: { kind: 'location', location_id: null, file_id: null, ssh_host_id: null, path: '' },
    dest: { kind: 'location', location_id: null, file_id: null, ssh_host_id: null, path: '' },
    overwrite: true,
    delete_source: false,
    preserve_mtime: true,
    verify_checksum: false,
    from_trigger: false,
  },
  env_plain: {},
  env_secrets: {},
  env_secret_refs: [],
  retry_count: 0,
  retry_base_delay_ms: 1000,
  retry_max_delay_ms: 30000,
  timeout_seconds: 3600,
  sla_missed_grace_seconds: 0,
  sla_late_seconds: 0,
  require_approval: false,
  approval_timeout_seconds: 0,
  concurrency_policy: 'allow',
  enabled: true,
  tags: [],
  watch_enabled: false,
  watch_target: 'local',
  watch_ssh_host_id: null,
  watch_agent_id: null,
  watch_location_id: null,
  watch_path: '',
  watch_interval_seconds: 60,
  notify_channel_ids: [],
  calendar_id: null,
}

const cronPresets: { expr: string; label: string }[] = [
  { expr: '*/5 * * * *', label: 'Every 5 min' },
  { expr: '0 * * * *', label: 'Hourly' },
  { expr: '0 2 * * *', label: 'Daily 02:00' },
  { expr: '0 9 * * 1-5', label: 'Weekdays 09:00' },
  { expr: '0 0 * * 0', label: 'Weekly' },
  { expr: '0 0 1 * *', label: 'Monthly' },
]

const sections: { id: string; label: string }[] = [
  { id: 'basics', label: 'Basics' },
  { id: 'schedule', label: 'Schedule' },
  { id: 'target', label: 'Where it runs' },
  { id: 'run', label: 'What it runs' },
  { id: 'env', label: 'Environment' },
  { id: 'reliability', label: 'Reliability' },
  { id: 'alerts', label: 'Alerts & SLA' },
  { id: 'approval', label: 'Approval' },
]

const ENV_KEY = /^[A-Za-z_][A-Za-z0-9_]*$/

function cronOK(expr: string, tz: string): boolean {
  try {
    new Cron(expr, { timezone: tz, paused: true })
    return true
  } catch {
    return false
  }
}

const emptyEndpoint = (kind: EndpointKind): TransferEndpoint => ({
  kind,
  location_id: null,
  file_id: null,
  ssh_host_id: null,
  agent_id: null,
  path: '',
})

function endpointOK(ep: TransferEndpoint, side: 'source' | 'dest', fromTrigger = false): boolean {
  // A trigger-driven source has no path of its own; every other side needs
  // one, except a location destination (empty means the base directory).
  const pathOK = ep.path.trim() !== '' || (side === 'source' && fromTrigger)
  if (ep.kind === 'location') return !!ep.location_id && (side === 'dest' || pathOK)
  if (ep.kind === 'library') return side === 'dest' || !!ep.file_id
  if (ep.kind === 'agent') return !!ep.agent_id && pathOK
  return !!ep.ssh_host_id && pathOK
}

type EnvRow = { k: string; v: string; mode: 'plain' | 'secret' | 'store'; store_id: string; path: string; key: string; stored?: boolean }

export default function JobEditor() {
  const { id } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { canWrite } = useAuth()
  const isNew = !id
  const { data: existing } = useJob(id)
  const createJob = useCreateJob()
  const updateJob = useUpdateJob()
  const runNow = useRunJob()
  const { data: agents = [] } = useAgents()
  const { data: hosts = [] } = useSSHHosts()
  const { data: locations = [] } = useStorageLocations()
  const { data: channels = [] } = useChannels()

  const [draft, setDraft] = useState<Draft>(emptyDraft)
  const [baseline, setBaseline] = useState('')
  const [files, setFiles] = useState<FileEntry[]>([])
  const [secretStores, setSecretStores] = useState<SecretStore[]>([])
  const [calendars, setCalendars] = useState<Calendar[]>([])
  const [tagInput, setTagInput] = useState('')
  const [dockerArgs, setDockerArgs] = useState('')
  const [saving, setSaving] = useState(false)
  const [showErrors, setShowErrors] = useState(false)

  useEffect(() => {
    if (existing) {
      const { id: _id, created_at: _c, updated_at: _u, next_run_at: _n, last_run_at: _l, workflow_id: _w, ...rest } = existing
      const d: Draft = {
        ...emptyDraft,
        ...rest,
        sla_missed_grace_seconds: rest.sla_missed_grace_seconds ?? 0,
        sla_late_seconds: rest.sla_late_seconds ?? 0,
        require_approval: rest.require_approval ?? false,
        approval_timeout_seconds: rest.approval_timeout_seconds ?? 0,
        env_secret_refs: rest.env_secret_refs ?? [],
        calendar_id: rest.calendar_id ?? null,
        notify_channel_ids: rest.notify_channel_ids ?? [],
      }
      setDraft(d)
      setDockerArgs(rest.docker_command.join(' '))
      setBaseline(JSON.stringify([d, rest.docker_command.join(' ')]))
    } else if (isNew) {
      setDraft(emptyDraft)
      setDockerArgs('')
      setBaseline(JSON.stringify([emptyDraft, '']))
    }
  }, [existing, isNew])

  useEffect(() => {
    api
      .listFiles()
      .then((r) => setFiles([...r.files].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())))
      .catch(() => undefined)
  }, [])
  useEffect(() => {
    api.listSecretStores().then((r) => setSecretStores(r.stores ?? [])).catch(() => undefined)
    api.listCalendars().then((r) => setCalendars(r.calendars ?? [])).catch(() => undefined)
  }, [])

  const dirty = baseline !== '' && JSON.stringify([draft, dockerArgs]) !== baseline
  useEffect(() => {
    if (!dirty) return
    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setDraft((d) => ({ ...d, [k]: v }))
  const isTransfer = draft.exec_mode === 'transfer'

  // ---- environment rows ----
  const envRows: EnvRow[] = [
    ...Object.entries(draft.env_plain).map(([k, v]) => ({ k, v, mode: 'plain' as const, store_id: '', path: '', key: '' })),
    ...Object.entries(draft.env_secrets).map(([k, v]) => ({ k, v, mode: 'secret' as const, store_id: '', path: '', key: '' })),
    ...(draft.env_secret_refs ?? []).map((r) => ({ k: r.name, v: '', mode: 'store' as const, store_id: r.store_id, path: r.path, key: r.key, stored: true })),
  ]
  const [pendingEnv, setPendingEnv] = useState<EnvRow[]>([])
  const allEnv = [...envRows, ...pendingEnv]
  const setEnv = (rows: EnvRow[]) => {
    const plain: Record<string, string> = {}
    const sec: Record<string, string> = {}
    const refs: { name: string; store_id: string; path: string; key: string }[] = []
    const pending: EnvRow[] = []
    for (const r of rows) {
      if (!r.k) {
        pending.push(r)
        continue
      }
      if (r.mode === 'store') refs.push({ name: r.k, store_id: r.store_id, path: r.path, key: r.key })
      else if (r.mode === 'secret') sec[r.k] = r.v
      else plain[r.k] = r.v
    }
    setPendingEnv(pending)
    setDraft((d) => ({ ...d, env_plain: plain, env_secrets: sec, env_secret_refs: refs }))
  }

  // ---- validation ----
  const errors: Record<string, string> = {}
  if (!draft.name.trim()) errors.name = 'Give the job a name.'
  if (draft.schedule_type === 'cron' && (!draft.cron_expr.trim() || !cronOK(draft.cron_expr, draft.timezone)))
    errors.cron = 'This is not a valid 5-field cron expression.'
  if (draft.schedule_type === 'interval' && draft.interval_seconds < 5) errors.interval = 'Use at least 5 seconds.'
  if (draft.schedule_type === 'oneoff' && !draft.oneoff_at) errors.oneoff = 'Pick a date and time.'
  if (!TZ_LIST.includes(draft.timezone)) errors.timezone = 'Pick a time zone from the list.'
  if (!isTransfer) {
    if (draft.target_type === 'agent' && !draft.target_selector.agent_id) errors.target = 'Choose an agent.'
    if (draft.target_type === 'ssh' && !draft.ssh_host_id) errors.target = 'Choose an SSH host.'
    if (draft.target_type === 'selector' && Object.entries(draft.target_selector).filter(([k, v]) => k !== 'agent_id' && v).length === 0)
      errors.target = 'Add at least one label.'
    if (draft.exec_mode === 'docker' && !draft.docker_image.trim()) errors.image = 'Name an image.'
  } else {
    const tc = draft.transfer_config
    if (!endpointOK(tc.source, 'source', !!tc.from_trigger) || !endpointOK(tc.dest, 'dest')) errors.transfer = 'Complete both ends of the transfer.'
    else if (tc.source.kind === 'library' && tc.dest.kind === 'library') errors.transfer = 'At least one end must be outside the library.'
    else if (tc.from_trigger && !draft.watch_enabled) errors.transfer = 'Turn on the file trigger to move the files it finds.'
  }
  const badKey = allEnv.find((r) => r.k && !ENV_KEY.test(r.k))
  if (badKey) errors.env = `${badKey.k} is not a valid variable name. Use letters, digits and _ only.`
  if (draft.watch_enabled) {
    if (!draft.watch_path.trim()) errors.watch = 'Give a path or glob to watch.'
    else if (draft.watch_interval_seconds < 10) errors.watch = 'Poll at most every 10 seconds.'
    else if (draft.watch_target === 'ssh' && !draft.watch_ssh_host_id) errors.watch = 'Choose the host to watch.'
    else if (draft.watch_target === 'agent' && !draft.watch_agent_id) errors.watch = 'Choose the agent to watch.'
    else if (draft.watch_target === 'location' && !draft.watch_location_id) errors.watch = 'Choose the storage location to watch.'
  }
  const errorCount = Object.keys(errors).length
  const err = (k: string) => (showErrors || k === 'cron' || k === 'env' ? errors[k] : undefined)

  // ---- schedule preview ----
  const preview = useMemo(() => {
    const now = new Date()
    const asJob = { ...draft, enabled: true, next_run_at: null } as unknown as Job
    return firingsBetween(asJob, now, new Date(now.getTime() + 60 * 86_400_000), 5)
  }, [draft])
  const sched = describeSchedule(draft)
  // Every time on this page is printed on one clock: the job's zone, or the
  // browser's for a one-off, whose date field is in the browser's zone.
  const zone = scheduleZone(draft)
  const zoneName = zone ?? Intl.DateTimeFormat().resolvedOptions().timeZone

  const leave = async () => {
    if (dirty && !(await confirm({ title: 'Discard changes?', body: 'Your edits to this job have not been saved.', confirmLabel: 'Discard', destructive: true })))
      return
    navigate('/jobs')
  }

  const save = async (andRun: boolean) => {
    if (errorCount > 0) {
      setShowErrors(true)
      toast.error(errorCount === 1 ? 'Fix the highlighted field first.' : `Fix the ${errorCount} highlighted fields first.`)
      return
    }
    setSaving(true)
    // The server derives where a transfer runs from its endpoints (the
    // agent that owns one, or the server); the editor only sends the ends.
    const payload: Partial<Job> = {
      ...draft,
      target_type: isTransfer ? 'local' : draft.target_type,
      target_selector: isTransfer ? {} : Object.fromEntries(Object.entries(draft.target_selector).filter(([k]) => k !== '')),
      docker_command: draft.exec_mode === 'docker' ? dockerArgs.split(/\s+/).filter(Boolean) : [],
      calendar_id: draft.calendar_id || null,
    }
    try {
      const saved = isNew ? await createJob.mutateAsync(payload) : await updateJob.mutateAsync({ id: id!, job: payload })
      setBaseline(JSON.stringify([draft, dockerArgs]))
      toast.success(isNew ? `Job "${saved.name}" created` : 'Job saved')
      if (andRun) {
        await runNow.mutateAsync(saved.id)
        toast.success('Run queued')
      }
      navigate('/jobs')
    } catch (e) {
      toast.error(String(e instanceof Error ? e.message : e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="anim-in pb-4">
      <nav aria-label="Breadcrumb" className="mb-2 flex items-center gap-1.5 text-[13px] text-faint">
        <Link to="/jobs" className="font-semibold hover:text-ink">
          Jobs
        </Link>
        <ChevronRight size={14} aria-hidden="true" />
        <span className="truncate">{isNew ? 'New job' : existing?.name ?? '…'}</span>
      </nav>
      <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink">
        {isNew ? 'New job' : draft.name || 'Edit job'}
      </h1>

      <div className="mt-6 grid gap-8 lg:grid-cols-[180px_minmax(0,1fr)] xl:grid-cols-[180px_minmax(0,1fr)_280px]">
        <nav aria-label="Sections" className="hidden lg:block">
          <ul className="sticky top-2 flex flex-col gap-0.5">
            {sections.map((s) => {
              const sectionErr =
                (s.id === 'basics' && errors.name) ||
                (s.id === 'schedule' && (errors.cron || errors.interval || errors.oneoff || errors.timezone || errors.watch)) ||
                (s.id === 'target' && errors.target) ||
                (s.id === 'run' && (errors.image || errors.transfer)) ||
                (s.id === 'env' && errors.env)
              return (
                <li key={s.id}>
                  <a
                    href={`#${s.id}`}
                    className="flex h-9 items-center gap-2 rounded-lg px-3 text-sm text-dim hover:bg-raised hover:text-ink"
                  >
                    <span className="flex-1">{s.label}</span>
                    {sectionErr && (showErrors || s.id === 'env' || s.id === 'schedule') && (
                      <span className="h-2 w-2 rounded-[1px] bg-danger" aria-label="has an error" />
                    )}
                  </a>
                </li>
              )
            })}
          </ul>
        </nav>

        <div className="min-w-0 space-y-5">
          <Card id="basics" title="Basics">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Name" error={err('name')}>
                <Input value={draft.name} onChange={(e) => set('name', e.target.value)} placeholder="Daily backup" autoFocus={isNew} />
              </Field>
              <Field label="Description">
                <Input value={draft.description} onChange={(e) => set('description', e.target.value)} placeholder="Optional" />
              </Field>
              <div className="sm:col-span-2">
                <Field label="Tags" hint="Press Enter to add. Tags group and filter jobs.">
                  <div className="flex min-h-10 flex-wrap items-center gap-1.5 rounded-md border border-input/70 bg-panel px-2 py-1.5 focus-within:border-accent focus-within:ring-3 focus-within:ring-accent/20">
                    {draft.tags.map((t) => (
                      <span key={t} className="inline-flex items-center gap-1 rounded-md bg-raised py-0.5 pr-1 pl-2 text-[13px] text-dim">
                        {t}
                        <button
                          type="button"
                          aria-label={`Remove tag ${t}`}
                          className="rounded p-0.5 text-faint hover:text-danger cursor-pointer"
                          onClick={() => set('tags', draft.tags.filter((x) => x !== t))}
                        >
                          <X size={12} />
                        </button>
                      </span>
                    ))}
                    <input
                      aria-label="Add a tag"
                      className="h-7 min-w-24 flex-1 bg-transparent px-1 text-sm text-ink placeholder:text-faint focus:outline-none"
                      placeholder={draft.tags.length ? '' : 'backup, nightly…'}
                      value={tagInput}
                      onChange={(e) => setTagInput(e.target.value)}
                      onKeyDown={(e) => {
                        if ((e.key === 'Enter' || e.key === ',') && tagInput.trim()) {
                          e.preventDefault()
                          set('tags', [...new Set([...draft.tags, tagInput.trim()])])
                          setTagInput('')
                        }
                      }}
                    />
                  </div>
                </Field>
              </div>
            </div>
          </Card>

          <Card id="schedule" title="Schedule">
            <div className="rounded-xl bg-accent-soft px-5 py-4 text-ink">
              <div className="font-display text-[20px] font-semibold tracking-[-0.01em]">
                {errors.cron ? 'Not a valid schedule yet' : `${sched.sentence} · ${zoneName}`}
              </div>
              <div className="mt-1 text-[13px] text-dim">
                {preview.length
                  ? `Next: ${preview.slice(0, 3).map((d) => fmtDayTime(d, zone)).join(' · ')}`
                  : 'No runs in the next 60 days.'}
                {!draft.enabled && ' (the job is paused)'}
              </div>
            </div>
            <div className="mt-4">
              <Tabs
                label="Schedule type"
                value={draft.schedule_type}
                onChange={(v) => set('schedule_type', v as ScheduleType)}
                tabs={[
                  { value: 'cron', label: 'Cron' },
                  { value: 'interval', label: 'Interval' },
                  { value: 'oneoff', label: 'Once' },
                  { value: 'manual', label: 'Manual' },
                ]}
              />
            </div>
            {draft.schedule_type === 'manual' && (
              <p className="mt-3 text-[13px] text-faint">
                No schedule of its own. The job runs when a workflow starts it, when its file trigger fires, or when you press Run. Use this for jobs
                inside a workflow and put the schedule on the <Link to="/workflows" className="underline">workflow</Link>.
              </p>
            )}
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              {draft.schedule_type === 'cron' && (
                <div className="space-y-3 sm:col-span-2">
                  <div className="flex flex-wrap gap-1.5" role="group" aria-label="Presets">
                    {cronPresets.map((p) => (
                      <button
                        key={p.expr}
                        type="button"
                        aria-pressed={draft.cron_expr === p.expr}
                        onClick={() => set('cron_expr', p.expr)}
                        className={cn(
                          'h-8 rounded-full border px-3 text-[13px] font-semibold transition-colors cursor-pointer',
                          draft.cron_expr === p.expr
                            ? 'border-ink bg-ink text-bg'
                            : 'border-input/60 text-dim hover:bg-raised',
                        )}
                      >
                        {p.label}
                      </button>
                    ))}
                  </div>
                  <Field label="Cron expression" hint="Minute, hour, day of month, month, day of week. 15 3 * * * runs daily at 03:15." error={err('cron')}>
                    <Input className="font-mono" value={draft.cron_expr} onChange={(e) => set('cron_expr', e.target.value)} />
                  </Field>
                </div>
              )}
              {draft.schedule_type === 'interval' && (
                <Field label="Every (seconds)" hint="3600 is hourly, 86400 is daily." error={err('interval')}>
                  <Input
                    type="number"
                    min={5}
                    className="font-mono"
                    value={draft.interval_seconds}
                    onChange={(e) => set('interval_seconds', Math.max(0, Number(e.target.value)))}
                  />
                </Field>
              )}
              {draft.schedule_type === 'oneoff' && (
                <Field label="Run at" hint="Runs once; the job stays so you can re-arm it later." error={err('oneoff')}>
                  <Input
                    type="datetime-local"
                    className="font-mono"
                    value={draft.oneoff_at ? toLocalInput(draft.oneoff_at) : ''}
                    onChange={(e) => set('oneoff_at', e.target.value ? new Date(e.target.value).toISOString() : null)}
                  />
                </Field>
              )}
              <Field label="Time zone" hint="Type to search." error={err('timezone')}>
                <Input list="tz-list" value={draft.timezone} onChange={(e) => set('timezone', e.target.value)} className="font-mono" />
              </Field>
              <datalist id="tz-list">
                {TZ_LIST.map((tz) => (
                  <option key={tz} value={tz} />
                ))}
              </datalist>
              <Field label="Skip calendar" hint="Skip scheduled runs on this calendar’s dates.">
                <Select value={draft.calendar_id ?? ''} onChange={(e) => set('calendar_id', e.target.value ? e.target.value : null)}>
                  <option value="">None</option>
                  {calendars.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>

            <div className="mt-5 border-t border-line-soft pt-4">
              <div className="flex items-center justify-between gap-4">
                <div>
                  <div className="text-sm font-semibold text-ink">Also run when a file appears</div>
                  <div className="text-[13px] text-faint">Polls a path or glob and runs once the matching files settle.</div>
                </div>
                <Switch label="Also run when a file appears" checked={draft.watch_enabled} onChange={(v) => set('watch_enabled', v)} />
              </div>
              {draft.watch_enabled && (
                <div className="mt-4 grid gap-4 sm:grid-cols-2">
                  <Field label="Watch on">
                    <Select value={draft.watch_target} onChange={(e) => set('watch_target', e.target.value as Job['watch_target'])}>
                      <option value="location">A storage location</option>
                      <option value="local">The Kairos server’s filesystem</option>
                      <option value="ssh">An SSH host (over SFTP)</option>
                      <option value="agent">An agent’s own filesystem</option>
                    </Select>
                  </Field>
                  {draft.watch_target === 'location' && (
                    <Field label="Storage location">
                      <Select value={draft.watch_location_id ?? ''} onChange={(e) => set('watch_location_id', e.target.value || null)}>
                        <option value="">Choose a location…</option>
                        {locations.map((l) => (
                          <option key={l.id} value={l.id}>
                            {l.name}
                          </option>
                        ))}
                      </Select>
                      {locations.length === 0 && (
                        <p className="mt-1.5 text-[12px] text-faint">
                          No locations yet — add one under <Link to="/storage" className="underline">Storage</Link>.
                        </p>
                      )}
                    </Field>
                  )}
                  {draft.watch_target === 'ssh' && (
                    <Field label="SSH host">
                      <Select value={draft.watch_ssh_host_id ?? ''} onChange={(e) => set('watch_ssh_host_id', e.target.value || null)}>
                        <option value="">Choose a host…</option>
                        {hosts.map((h) => (
                          <option key={h.id} value={h.id}>
                            {h.name} ({h.host})
                          </option>
                        ))}
                      </Select>
                    </Field>
                  )}
                  {draft.watch_target === 'agent' && (
                    <Field label="Agent">
                      <Select value={draft.watch_agent_id ?? ''} onChange={(e) => set('watch_agent_id', e.target.value || null)}>
                        <option value="">Choose an agent…</option>
                        {agents.map((ag) => (
                          <option key={ag.id} value={ag.id}>
                            {ag.name} {ag.online ? '' : '(offline)'}
                          </option>
                        ))}
                      </Select>
                    </Field>
                  )}
                  <Field
                    label="Path or glob"
                    hint={draft.watch_target === 'location' ? 'Relative to the location, for example incoming/*.csv' : 'For example /data/incoming/*.csv'}
                    error={err('watch')}
                  >
                    <Input
                      className="font-mono"
                      value={draft.watch_path}
                      onChange={(e) => set('watch_path', e.target.value)}
                      placeholder={draft.watch_target === 'location' ? 'incoming/*.csv' : '/data/incoming/*.csv'}
                    />
                  </Field>
                  <Field label="Check every (seconds)" hint="At least 10.">
                    <Input
                      type="number"
                      min={10}
                      className="font-mono"
                      value={draft.watch_interval_seconds}
                      onChange={(e) => set('watch_interval_seconds', Math.max(0, Number(e.target.value)))}
                    />
                  </Field>
                </div>
              )}
            </div>
          </Card>

          <Card id="target" title="Where it runs">
            {isTransfer ? (
              <p className="text-sm text-faint">
                File transfers are run by the Kairos server, or by the agent on either end. Pick the ends under What it runs.
              </p>
            ) : (
              <div className="space-y-4">
                <div role="radiogroup" aria-label="Target" className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-4">
                  {(
                    [
                      ['agent', 'An agent', 'A machine running the Kairos agent'],
                      ['ssh', 'An SSH host', 'Kairos connects over SSH'],
                      ['selector', 'Agents by label', 'Every agent matching labels'],
                      ['local', 'Kairos server', 'Runs next to Kairos itself'],
                    ] as [Job['target_type'], string, string][]
                  ).map(([v, label, blurb]) => (
                    <button
                      key={v}
                      type="button"
                      role="radio"
                      aria-checked={draft.target_type === v}
                      onClick={() => set('target_type', v)}
                      className={cn(
                        'rounded-xl border p-3.5 text-left transition-colors cursor-pointer',
                        draft.target_type === v ? 'border-accent bg-accent-soft/60 ring-1 ring-accent' : 'border-line hover:bg-raised',
                      )}
                    >
                      <div className="text-sm font-semibold text-ink">{label}</div>
                      <div className="mt-0.5 text-[12.5px] text-faint">{blurb}</div>
                    </button>
                  ))}
                </div>
                {draft.target_type === 'agent' && (
                  <Field label="Agent" error={err('target')}>
                    <Select value={draft.target_selector.agent_id ?? ''} onChange={(e) => set('target_selector', { agent_id: e.target.value })}>
                      <option value="">Choose an agent…</option>
                      {agents.map((ag) => (
                        <option key={ag.id} value={ag.id}>
                          {ag.name} {ag.online ? '' : '(offline)'}
                        </option>
                      ))}
                    </Select>
                  </Field>
                )}
                {draft.target_type === 'ssh' && (
                  <Field label="SSH host" error={err('target')}>
                    <Select value={draft.ssh_host_id ?? ''} onChange={(e) => set('ssh_host_id', e.target.value || null)}>
                      <option value="">Choose a host…</option>
                      {hosts.map((h) => (
                        <option key={h.id} value={h.id}>
                          {h.name} ({h.host})
                        </option>
                      ))}
                    </Select>
                  </Field>
                )}
                {draft.target_type === 'selector' && (
                  <>
                    <LabelEditor
                      rows={draft.target_selector}
                      onChange={(rows) => {
                        const obj: Record<string, string> = {}
                        for (const r of rows) obj[r.k] = r.v
                        set('target_selector', obj)
                      }}
                    />
                    {err('target') && <p className="text-xs font-medium text-danger">{err('target')}</p>}
                  </>
                )}
                {((draft.target_type === 'agent' && agents.length === 0) || (draft.target_type === 'ssh' && hosts.length === 0)) && (
                  <p className="text-[13px] text-faint">
                    Nothing connected yet.{' '}
                    <Link to="/targets" className="font-semibold text-accent-ink hover:underline">
                      Add a host or agent
                    </Link>
                  </p>
                )}
              </div>
            )}
          </Card>

          <Card id="run" title="What it runs">
            <Tabs
              label="Job kind"
              value={draft.exec_mode}
              onChange={(v) => setDraft((d) => ({ ...d, exec_mode: v as ExecMode, target_type: v === 'transfer' ? 'local' : d.target_type }))}
              tabs={[
                { value: 'shell', label: <KindLabel icon={Terminal} text="Shell script" /> },
                { value: 'docker', label: <KindLabel icon={Box} text="Docker" /> },
                { value: 'transfer', label: <KindLabel icon={ArrowLeftRight} text="File transfer" /> },
              ]}
            />
            <div className="mt-4">
              {draft.exec_mode === 'shell' && (
                <div className="grid gap-4">
                  <Field label="Script" hint="Runs with /bin/sh -c.">
                    <Textarea
                      rows={9}
                      spellCheck={false}
                      className="bg-terminal font-mono text-[13px] leading-relaxed text-terminal-ink"
                      value={draft.shell_script}
                      onChange={(e) => set('shell_script', e.target.value)}
                    />
                  </Field>
                  <Field label="Working directory" hint="Optional.">
                    <Input className="font-mono" value={draft.workdir} onChange={(e) => set('workdir', e.target.value)} placeholder="/srv/app" />
                  </Field>
                </div>
              )}
              {draft.exec_mode === 'docker' && (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field label="Image" error={err('image')}>
                    <Input className="font-mono" value={draft.docker_image} onChange={(e) => set('docker_image', e.target.value)} placeholder="alpine:3.20" />
                  </Field>
                  <Field label="Command and arguments">
                    <Input className="font-mono" value={dockerArgs} onChange={(e) => setDockerArgs(e.target.value)} placeholder='sh -c "echo hi"' />
                  </Field>
                  <div className="sm:col-span-2">
                    <Field label="Working directory" hint="Optional; defaults to the image’s.">
                      <Input className="font-mono" value={draft.workdir} onChange={(e) => set('workdir', e.target.value)} placeholder="/app" />
                    </Field>
                  </div>
                </div>
              )}
              {isTransfer && (
                <div className="space-y-3">
                  <EndpointPicker
                    side="source"
                    ep={draft.transfer_config.source}
                    hosts={hosts}
                    agents={agents}
                    files={files}
                    locations={locations}
                    fromTrigger={!!draft.transfer_config.from_trigger}
                    onChange={(ep) => set('transfer_config', { ...draft.transfer_config, source: ep })}
                  />
                  <div className="flex justify-center text-faint">
                    <ArrowRight size={16} className="rotate-90" aria-hidden="true" />
                  </div>
                  <EndpointPicker
                    side="dest"
                    ep={draft.transfer_config.dest}
                    hosts={hosts}
                    agents={agents}
                    files={files}
                    locations={locations}
                    onChange={(ep) => set('transfer_config', { ...draft.transfer_config, dest: ep })}
                  />
                  {err('transfer') && <p className="text-xs font-medium text-danger">{err('transfer')}</p>}
                  <div className="grid gap-3 pt-1 sm:grid-cols-2">
                    <ToggleRow
                      label="Overwrite"
                      hint="Replace an existing file at the destination."
                      checked={draft.transfer_config.overwrite}
                      onChange={(v) => set('transfer_config', { ...draft.transfer_config, overwrite: v })}
                    />
                    <ToggleRow
                      label="Move"
                      hint="Delete each source file after its copy is in place."
                      checked={draft.transfer_config.delete_source}
                      onChange={(v) => set('transfer_config', { ...draft.transfer_config, delete_source: v })}
                    />
                    <ToggleRow
                      label="Keep modification time"
                      hint="The copy gets the source's mtime."
                      checked={!!draft.transfer_config.preserve_mtime}
                      onChange={(v) => set('transfer_config', { ...draft.transfer_config, preserve_mtime: v })}
                    />
                    <ToggleRow
                      label="Verify checksum"
                      hint="Read the copy back and compare SHA-256. Slower; catches a bad link."
                      checked={!!draft.transfer_config.verify_checksum}
                      onChange={(v) => set('transfer_config', { ...draft.transfer_config, verify_checksum: v })}
                    />
                    <ToggleRow
                      label="Use the files from the trigger"
                      hint="Move exactly the files the file trigger found, from where it watches."
                      checked={!!draft.transfer_config.from_trigger}
                      onChange={(v) => set('transfer_config', { ...draft.transfer_config, from_trigger: v })}
                    />
                  </div>
                </div>
              )}
            </div>
          </Card>

          {!isTransfer && (
            <Card
              id="env"
              title="Environment"
              right={
                <Button size="sm" variant="secondary" onClick={() => setEnv([...allEnv, { k: '', v: '', mode: 'plain', store_id: '', path: '', key: '' }])}>
                  <Plus size={14} /> Add variable
                </Button>
              }
            >
              {allEnv.length === 0 && <p className="text-sm text-faint">No variables. Secret values are encrypted at rest and never shown again.</p>}
              <div className="space-y-2.5">
                {allEnv.map((r, i) => {
                  const update = (patch: Partial<EnvRow>) => {
                    const rows = [...allEnv]
                    rows[i] = { ...rows[i], ...patch }
                    setEnv(rows)
                  }
                  return (
                    <div key={i} className="flex flex-wrap items-start gap-2">
                      <Input
                        aria-label="Variable name"
                        value={r.k}
                        placeholder="NAME"
                        aria-invalid={!!r.k && !ENV_KEY.test(r.k)}
                        className="w-full font-mono sm:w-52"
                        onChange={(e) => update({ k: e.target.value })}
                      />
                      <Select
                        aria-label="Value source"
                        wrapperClassName="w-44"
                        value={r.mode}
                        onChange={(e) => update({ mode: e.target.value as EnvRow['mode'], v: '' })}
                      >
                        <option value="plain">Plain value</option>
                        <option value="secret">Secret</option>
                        <option value="store">From a secret store</option>
                      </Select>
                      {r.mode === 'store' ? (
                        <>
                          <Select aria-label="Secret store" wrapperClassName="w-40" value={r.store_id} onChange={(e) => update({ store_id: e.target.value })}>
                            <option value="">Store…</option>
                            {secretStores.map((s) => (
                              <option key={s.id} value={s.id}>
                                {s.name}
                              </option>
                            ))}
                          </Select>
                          <Input aria-label="Secret path" value={r.path} placeholder="path" className="min-w-0 flex-1 font-mono" onChange={(e) => update({ path: e.target.value })} />
                          <Input aria-label="Secret key" value={r.key} placeholder="key" className="w-28 font-mono" onChange={(e) => update({ key: e.target.value })} />
                        </>
                      ) : (
                        <Input
                          aria-label={`Value of ${r.k || 'variable'}`}
                          value={r.v}
                          placeholder={r.mode === 'secret' ? 'Secret value' : 'Value'}
                          type={r.mode === 'secret' ? 'password' : 'text'}
                          autoComplete="off"
                          className="min-w-0 flex-1 font-mono"
                          onChange={(e) => update({ v: e.target.value })}
                        />
                      )}
                      <Button size="icon" variant="ghost" aria-label={`Remove ${r.k || 'variable'}`} onClick={() => setEnv(allEnv.filter((_, x) => x !== i))}>
                        <Trash2 size={15} className="text-danger" />
                      </Button>
                    </div>
                  )
                })}
              </div>
              {err('env') && <p className="mt-2 text-xs font-medium text-danger">{err('env')}</p>}
            </Card>
          )}

          <Card id="reliability" title="Reliability">
            <div className="grid gap-4 sm:grid-cols-3">
              <Field label="Retries" hint="Extra attempts after a failure.">
                <Input type="number" min={0} max={10} className="font-mono" value={draft.retry_count} onChange={(e) => set('retry_count', Number(e.target.value))} />
              </Field>
              <Field label="Timeout (seconds)" hint="A run longer than this is stopped.">
                <Input type="number" min={5} className="font-mono" value={draft.timeout_seconds} onChange={(e) => set('timeout_seconds', Number(e.target.value))} />
              </Field>
              <Field label="If still running">
                <Select value={draft.concurrency_policy} onChange={(e) => set('concurrency_policy', e.target.value)}>
                  <option value="allow">Run in parallel</option>
                  <option value="forbid">Skip the new run</option>
                  <option value="replace">Stop the old run</option>
                </Select>
              </Field>
            </div>
            <div className="mt-4 border-t border-line-soft pt-4">
              <ToggleRow label="Enabled" hint="A paused job never runs on its schedule. You can still run it by hand." checked={draft.enabled} onChange={(v) => set('enabled', v)} />
            </div>
          </Card>

          <Card id="alerts" title="Alerts & SLA">
            <div className="text-sm font-semibold text-ink">Alert channels</div>
            <div className="mb-2.5 text-[13px] text-faint">Failures alert every channel unless you pick some here.</div>
            {channels.length === 0 ? (
              <p className="text-[13px] text-faint">
                No channels yet.{' '}
                <Link to="/notifications" className="font-semibold text-accent-ink hover:underline">
                  Add Slack, email or a webhook
                </Link>
              </p>
            ) : (
              <div className="flex flex-wrap gap-1.5" role="group" aria-label="Alert channels">
                {channels.map((ch) => {
                  const ids = draft.notify_channel_ids ?? []
                  const on = ids.includes(ch.id)
                  return (
                    <button
                      key={ch.id}
                      type="button"
                      aria-pressed={on}
                      onClick={() => set('notify_channel_ids', on ? ids.filter((x) => x !== ch.id) : [...ids, ch.id])}
                      className={cn(
                        'h-8 rounded-full border px-3 text-[13px] font-semibold transition-colors cursor-pointer',
                        on ? 'border-ink bg-ink text-bg' : 'border-input/60 text-dim hover:bg-raised',
                      )}
                    >
                      {ch.name}
                    </button>
                  )
                })}
              </div>
            )}
            <div className="mt-5 grid gap-4 border-t border-line-soft pt-4 sm:grid-cols-2">
              <Field label="Alert if it hasn’t started after (seconds)" hint="0 turns it off.">
                <Input
                  type="number"
                  min={0}
                  className="font-mono"
                  value={draft.sla_missed_grace_seconds ?? 0}
                  onChange={(e) => set('sla_missed_grace_seconds', Number(e.target.value))}
                />
              </Field>
              <Field label="Alert if a run takes longer than (seconds)" hint="Only alerts; the timeout still stops the run. 0 turns it off.">
                <Input
                  type="number"
                  min={0}
                  className="font-mono"
                  value={draft.sla_late_seconds ?? 0}
                  onChange={(e) => set('sla_late_seconds', Number(e.target.value))}
                />
              </Field>
            </div>
          </Card>

          <Card id="approval" title="Approval">
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex items-center justify-between gap-4 rounded-xl border border-line-soft p-3.5">
                <div>
                  <div className="text-sm font-semibold text-ink">Require approval</div>
                  <div className="text-[13px] text-faint">Each run waits until someone approves it.</div>
                </div>
                <Switch label="Require approval" checked={!!draft.require_approval} onChange={(v) => set('require_approval', v)} />
              </div>
              <Field label="Give up after (seconds)" hint="A run still waiting this long is skipped. 0 waits forever.">
                <Input
                  type="number"
                  min={0}
                  className="font-mono"
                  value={draft.approval_timeout_seconds ?? 0}
                  onChange={(e) => set('approval_timeout_seconds', Number(e.target.value))}
                />
              </Field>
            </div>
          </Card>
        </div>

        <aside className="hidden xl:block">
          <div className="sticky top-2 space-y-4">
            <div className="rounded-2xl border border-line bg-panel p-5">
              <h2 className="text-[13px] font-semibold text-faint">Next runs, {zoneName} time</h2>
              {preview.length === 0 ? (
                <p className="mt-2 text-sm text-faint">None in the next 60 days.</p>
              ) : (
                <ol className="mt-2 space-y-1.5">
                  {preview.map((d) => (
                    <li key={d.getTime()} className="text-sm text-ink tabular-nums">
                      {fmtDayTime(d, zone)}
                    </li>
                  ))}
                </ol>
              )}
            </div>
            {!isNew && id && <RecentRuns jobId={id} />}
          </div>
        </aside>
      </div>

      <div className="sticky bottom-0 z-10 -mx-5 mt-6 flex flex-wrap items-center gap-3 border-t border-line bg-bg/95 px-5 py-3.5 backdrop-blur md:-mx-9 md:px-9">
        <div className="mr-auto text-[13px]" aria-live="polite">
          {dirty ? <span className="font-semibold text-ink">Unsaved changes</span> : <span className="text-faint">{isNew ? 'New job' : 'No changes'}</span>}
          {showErrors && errorCount > 0 && (
            <span className="ml-2 font-semibold text-danger">
              · {errorCount} {errorCount === 1 ? 'error' : 'errors'}
            </span>
          )}
        </div>
        <Button variant="ghost" onClick={leave}>
          Cancel
        </Button>
        {canWrite && (
          <>
            <Button variant="secondary" disabled={saving} onClick={() => save(true)}>
              Save &amp; run now
            </Button>
            <Button variant="primary" disabled={saving} onClick={() => save(false)}>
              {saving ? 'Saving…' : isNew ? 'Create job' : 'Save job'}
            </Button>
          </>
        )}
      </div>
    </div>
  )
}

function Card({ id, title, right, children }: { id: string; title: string; right?: ReactNode; children: ReactNode }) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} className="scroll-mt-4 rounded-2xl border border-line bg-panel">
      <div className="flex items-center justify-between gap-3 border-b border-line-soft px-5 py-3.5">
        <h2 id={`${id}-title`} className="flex items-center gap-2 font-display text-[18px] font-semibold tracking-[-0.01em] text-ink">
          {title}
        </h2>
        {right}
      </div>
      <div className="p-5">{children}</div>
    </section>
  )
}

function KindLabel({ icon: Icon, text }: { icon: typeof Terminal; text: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <Icon size={14} aria-hidden="true" />
      {text}
    </span>
  )
}

function ToggleRow({ label, hint, checked, onChange }: { label: string; hint: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div>
        <div className="text-sm font-semibold text-ink">{label}</div>
        <div className="text-[13px] text-faint">{hint}</div>
      </div>
      <Switch label={label} checked={checked} onChange={onChange} />
    </div>
  )
}

function RecentRuns({ jobId }: { jobId: string }) {
  const { data: runs } = useRuns({ job_id: jobId, limit: '6' })
  return (
    <div className="rounded-2xl border border-line bg-panel p-5">
      <h2 className="text-[13px] font-semibold text-faint">Recent runs</h2>
      {!runs?.length ? (
        <p className="mt-2 text-sm text-faint">Not run yet.</p>
      ) : (
        <ul className="mt-2 space-y-2">
          {runs.map((r) => (
            <li key={r.id}>
              <Link to={`/runs/${r.id}`} className="flex items-center justify-between gap-2 hover:underline">
                <StatusBadge status={r.status} />
                <span className="text-[13px] text-faint">{timeAgo(r.created_at)}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// EndpointPicker picks one side of a transfer route: a storage location,
// the managed library, or (for older jobs) an SSH host or agent path.
function EndpointPicker({
  side,
  ep,
  hosts,
  agents,
  files,
  locations,
  fromTrigger = false,
  onChange,
}: {
  side: 'source' | 'dest'
  ep: TransferEndpoint
  hosts: SSHHost[]
  agents: AgentInfo[]
  files: FileEntry[]
  locations: StorageLocation[]
  fromTrigger?: boolean
  onChange: (ep: TransferEndpoint) => void
}) {
  const legacy = ep.kind === 'ssh' || ep.kind === 'agent'
  const kinds: EndpointKind[] = legacy ? ['location', 'library', ep.kind] : ['location', 'library']
  const kindLabel: Record<EndpointKind, string> = { location: 'Storage location', library: 'File library', ssh: 'SSH host path', agent: 'Agent path' }
  const isSource = side === 'source'
  return (
    <div className="rounded-xl border border-line-soft bg-bg/50 p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <span className="text-[13px] font-semibold text-dim">{isSource ? 'From' : 'To'}</span>
        <div className="flex gap-1.5">
          {kinds.map((k) => (
            <button
              key={k}
              onClick={() => onChange(emptyEndpoint(k))}
              type="button"
              aria-pressed={ep.kind === k}
              className={cn(
                'h-8 rounded-full border px-3 text-[13px] font-semibold transition-colors cursor-pointer',
                ep.kind === k ? 'border-ink bg-ink text-bg' : 'border-input/60 text-dim hover:bg-raised',
              )}
            >
              {kindLabel[k]}
            </button>
          ))}
        </div>
      </div>

      {ep.kind === 'location' ? (
        <div className="grid gap-3 sm:grid-cols-[1fr_1.2fr]">
          <Field label="Location">
            <Select value={ep.location_id ?? ''} onChange={(e) => onChange({ ...ep, location_id: e.target.value || null })}>
              <option value="">Choose a location…</option>
              {locations.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </Select>
            {locations.length === 0 && (
              <p className="mt-1.5 text-[12px] text-faint">
                No locations yet — add one under <Link to="/storage" className="underline">Storage</Link>.
              </p>
            )}
          </Field>
          {isSource && fromTrigger ? (
            <Field label="Files" hint="Whatever the file trigger matched, from the location it watches.">
              <Input className="font-mono" value="from the trigger" disabled readOnly />
            </Field>
          ) : (
            <Field
              label={isSource ? 'File or glob' : 'Destination'}
              hint={isSource ? 'Relative to the location. A glob copies every match.' : 'A file name, or a directory ending in / to keep source names.'}
            >
              <Input
                className="font-mono"
                value={ep.path}
                onChange={(e) => onChange({ ...ep, path: e.target.value })}
                placeholder={isSource ? 'incoming/*.csv' : 'archive/'}
              />
            </Field>
          )}
        </div>
      ) : ep.kind === 'library' ? (
        isSource ? (
          <Field label="Library file">
            <Select value={ep.file_id ?? ''} onChange={(e) => onChange({ ...ep, file_id: e.target.value || null })}>
              <option value="">select a staged file…</option>
              {files.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name} · {fmtDateTime(f.created_at)}
                </option>
              ))}
            </Select>
            {files.length === 0 && (
              <p className="mt-1.5 text-[12px] text-faint">
                Nothing staged yet — upload on the Files page first.
              </p>
            )}
          </Field>
        ) : (
          <Field label="Save as filename" hint="Blank keeps the source filename.">
            <Input className="font-mono" value={ep.path} onChange={(e) => onChange({ ...ep, path: e.target.value })} placeholder="report.csv" />
          </Field>
        )
      ) : ep.kind === 'ssh' ? (
        <div className="grid gap-3 sm:grid-cols-[1fr_1.2fr]">
          <Field label="SSH host" hint="Older style. A storage location does the same with a base directory.">
            <Select value={ep.ssh_host_id ?? ''} onChange={(e) => onChange({ ...ep, ssh_host_id: e.target.value || null })}>
              <option value="">select a host…</option>
              {hosts.map((h) => (
                <option key={h.id} value={h.id}>
                  {h.name} ({h.host})
                </option>
              ))}
            </Select>
          </Field>
          <Field label={isSource ? 'Remote path' : 'Destination path'}>
            <Input
              className="font-mono"
              value={ep.path}
              onChange={(e) => onChange({ ...ep, path: e.target.value })}
              placeholder={isSource ? '/var/data/report.csv' : '/backups/report.csv'}
            />
          </Field>
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-[1fr_1.2fr]">
          <Field label="Agent" hint="Older style. An agent storage location does the same with a base directory.">
            <Select value={ep.agent_id ?? ''} onChange={(e) => onChange({ ...ep, agent_id: e.target.value || null })}>
              <option value="">select an agent…</option>
              {agents.map((ag) => (
                <option key={ag.id} value={ag.id}>
                  {ag.name} {ag.online ? '(online)' : '(offline)'}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={isSource ? 'Path on agent' : 'Destination path'}>
            <Input
              className="font-mono"
              value={ep.path}
              onChange={(e) => onChange({ ...ep, path: e.target.value })}
              placeholder={isSource ? '/var/data/report.csv' : '/backups/report.csv'}
            />
          </Field>
        </div>
      )}
    </div>
  )
}

function toLocalInput(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

interface Row {
  k: string
  v: string
}

function LabelEditor({ rows, onChange }: { rows: Record<string, string>; onChange: (r: Row[]) => void }) {
  const list: Row[] = Object.entries(rows).map(([k, v]) => ({ k, v }))
  return (
    <Field label="Match agents with labels" hint="Agents whose labels include all of these receive the run. At least one label required.">
      <div className="space-y-2">
        {list.map((r, i) => (
          <div key={i} className="flex gap-2">
            <Input
              className="w-2/5 font-mono"
              aria-label="Label name"
              placeholder="role"
              value={r.k}
              onChange={(e) => {
                const next = [...list]
                next[i].k = e.target.value
                onChange(next)
              }}
            />
            <Input
              className="flex-1 font-mono"
              aria-label="Label value"
              placeholder="worker"
              value={r.v}
              onChange={(e) => {
                const next = [...list]
                next[i].v = e.target.value
                onChange(next)
              }}
            />
            <Button size="icon" variant="ghost" aria-label="Remove label" onClick={() => onChange(list.filter((_, x) => x !== i))}>
              <Trash2 size={15} className="text-danger" />
            </Button>
          </div>
        ))}
        <Button size="sm" variant="secondary" onClick={() => onChange([...list, { k: '', v: '' }])}>
          <Plus size={13} /> Add label
        </Button>
      </div>
    </Field>
  )
}
