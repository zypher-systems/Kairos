import { useCallback, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { ArrowRight, GitBranch, Play, Plus, Trash2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Button, Input, Field, Dialog, Drawer, EmptyState, Switch, QueryError, Select, Tabs, Textarea, RowMenu, MenuAction } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { cn, targetLabel, timeAgo } from '@/lib/utils'
import { TZ_LIST, type Calendar, type Job, type ScheduleType, type Workflow } from '@/lib/types'
import { useStorageLocations } from '@/hooks/queries'
import { describeSchedule, relFuture, scheduleZone } from '@/lib/schedule'
import { WriteGate } from '@/lib/auth'

export default function WorkflowsPage() {
  const confirm = useConfirm()
  const [workflows, setWorkflows] = useState<Workflow[]>()
  const [jobs, setJobs] = useState<Job[]>([])
  const [editing, setEditing] = useState<string | null>(null)
  const [error, setError] = useState<unknown>()

  const reload = useCallback(() => {
    setError(undefined)
    api
      .listWorkflows()
      .then((r) => setWorkflows(r.workflows))
      .catch((e) => setError(e))
    api.listJobs({ limit: '500' }).then((r) => setJobs(r.jobs)).catch((e) => setError(e))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Workflows</h1>
          <p className="mt-1.5 text-sm text-faint">Chains of jobs. When one finishes, the next one starts, depending on how it went.</p>
        </div>
        <WriteGate>
          <NewWorkflowDialog
            onDone={(id) => {
              reload()
              setEditing(id)
            }}
          />
        </WriteGate>
      </header>

      {error != null ? <QueryError error={error} /> : null}

      <div className="rounded-2xl border border-line bg-panel">
        {!workflows && !error && <div className="py-12 text-center text-sm text-faint">Loading…</div>}
        {workflows && workflows.length === 0 && (
          <EmptyState
            icon={GitBranch}
            title="No workflows yet"
            sub="Link jobs together: when one finishes, the next can start on success, on failure, or either way."
          />
        )}
        <ul>
          {workflows?.map((wf) => (
            <li key={wf.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line-soft px-5 py-4 last:border-b-0">
              <span className={cn('h-2.5 w-2.5 shrink-0', wf.enabled ? 'rounded-full bg-ok' : 'rounded-[2px] bg-faint')} aria-hidden="true" />
              <button type="button" onClick={() => setEditing(wf.id)} className="min-w-0 flex-1 text-left cursor-pointer">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={cn('text-[15px] font-semibold hover:underline', wf.enabled ? 'text-ink' : 'text-faint')}>{wf.name}</span>
                  <span className="rounded-md bg-raised px-2 py-px text-xs text-dim">
                    {wf.job_count ?? 0} {wf.job_count === 1 ? 'job' : 'jobs'}
                  </span>
                  <span className={cn('text-[13px] font-semibold', wf.enabled ? 'text-ok' : 'text-faint')}>{wf.enabled ? 'On' : 'Paused'}</span>
                  {wf.schedule_type && wf.schedule_type !== 'manual' && (
                    <span className="text-[13px] text-dim">
                      · {describeSchedule(wf).sentence}
                      {wf.next_run_at && wf.enabled ? ` · next ${relFuture(wf.next_run_at, Date.now(), scheduleZone(wf))}` : ''}
                    </span>
                  )}
                  {wf.watch_enabled && <span className="text-[13px] text-dim">· file trigger</span>}
                </div>
                <div className="mt-0.5 text-[13px] text-faint">
                  {wf.description || 'No description'} · created {timeAgo(wf.created_at)}
                </div>
              </button>
              <WriteGate>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={!wf.enabled}
                    aria-label={`Run ${wf.name} now`}
                    onClick={() =>
                      api
                        .runWorkflow(wf.id)
                        .then((r) => toast.success(`Started ${r.fired} first job${r.fired === 1 ? '' : 's'}`))
                        .catch((e) => toast.error(String(e)))
                    }
                  >
                    <Play size={12} fill="currentColor" aria-hidden="true" /> Run
                  </Button>
                  <RowMenu label={`Actions for ${wf.name}`}>
                    <MenuAction onSelect={() => setEditing(wf.id)}>Edit</MenuAction>
                    <MenuAction danger onSelect={() =>
                        confirm({
                          title: `Delete workflow ${wf.name}?`,
                          body: 'Its jobs are kept; only the links between them are removed.',
                          confirmLabel: 'Delete workflow',
                          destructive: true,
                        }).then(
                          (ok) =>
                            void (ok &&
                              api
                                .deleteWorkflow(wf.id)
                                .then(() => {
                                  toast.success(`${wf.name} deleted`)
                                  reload()
                                })
                                .catch((e) => toast.error(String(e)))),
                        )
                      }
                    >
                      Delete…
                    </MenuAction>
                  </RowMenu>
                </div>
              </WriteGate>
            </li>
          ))}
        </ul>
      </div>

      {editing && (
        <WorkflowEditor
          workflowId={editing}
          jobs={jobs}
          onClose={() => {
            setEditing(null)
            reload()
          }}
        />
      )}
    </div>
  )
}

function NewWorkflowDialog({ onDone }: { onDone: (id: string) => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  const create = () => {
    api
      .createWorkflow({ name, description })
      .then((wf) => {
        toast.success(`Workflow "${wf.name}" created`)
        setOpen(false)
        setName('')
        setDescription('')
        onDone(wf.id)
      })
      .catch((e) => toast.error(String(e)))
  }

  return (
    <>
      <Button variant="primary" onClick={() => setOpen(true)}>
        <Plus size={16} strokeWidth={2.4} /> New workflow
      </Button>
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        title="New workflow"
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" disabled={!name.trim()} onClick={create}>
              Create
            </Button>
          </>
        }
      >
        <div className="grid gap-4">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="nightly-etl" autoFocus />
          </Field>
          <Field label="Description">
            <Input value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Optional" />
          </Field>
        </div>
      </Dialog>
    </>
  )
}

// ---------------------------------------------------------------- editor

type Cond = 'success' | 'failure' | 'always'
const condLabel: Record<Cond, string> = { success: 'On success', failure: 'On failure', always: 'Either way' }
// Colour plus shape: failure links use a square marker.
const condDot: Record<Cond, string> = {
  success: 'bg-ok rounded-full',
  failure: 'bg-danger rounded-[1px]',
  always: 'bg-cyan rounded-full',
}

interface DraftEdge {
  key: string
  from: string
  to: string
  condition: Cond
}

// WorkflowEditor lays jobs out in columns by depth (first jobs on the left)
// with each job's outgoing links listed on its card.
function WorkflowEditor({ workflowId, jobs, onClose }: { workflowId: string; jobs: Job[]; onClose: () => void }) {
  const [edges, setEdges] = useState<DraftEdge[]>([])
  const [wfName, setWfName] = useState('')
  const [wfDesc, setWfDesc] = useState('')
  const [wfEnabled, setWfEnabled] = useState(true)
  const [wfParams, setWfParams] = useState('')
  const [schedType, setSchedType] = useState<ScheduleType>('manual')
  const [cronExpr, setCronExpr] = useState('0 3 * * *')
  const [intervalSec, setIntervalSec] = useState(3600)
  const [oneoffAt, setOneoffAt] = useState<string | null>(null)
  const [timezone, setTimezone] = useState('UTC')
  const [calendarId, setCalendarId] = useState('')
  const [watchOn, setWatchOn] = useState(false)
  const [watchLoc, setWatchLoc] = useState('')
  const [watchPath, setWatchPath] = useState('')
  const [watchEvery, setWatchEvery] = useState(60)
  const { data: locations = [] } = useStorageLocations()
  const [calendars, setCalendars] = useState<Calendar[]>([])
  useEffect(() => {
    api.listCalendars().then((r) => setCalendars(r.calendars ?? [])).catch(() => undefined)
  }, [])
  const [memberIDs, setMemberIDs] = useState<Set<string>>(new Set())
  const [saving, setSaving] = useState(false)
  const [addFrom, setAddFrom] = useState('')
  const [addTo, setAddTo] = useState('')
  const [addCond, setAddCond] = useState<Cond>('success')

  const jobName = useMemo(() => {
    const m = new Map<string, string>()
    for (const j of jobs) m.set(j.id, j.name)
    return (id: string) => m.get(id) ?? id.slice(0, 8)
  }, [jobs])

  const reload = useCallback(() => {
    api
      .getWorkflow(workflowId)
      .then((d) => {
        setWfName(d.name)
        setWfDesc(d.description)
        setWfEnabled(d.enabled)
        setSchedType(d.schedule_type || 'manual')
        setCronExpr(d.cron_expr || '0 3 * * *')
        setIntervalSec(d.interval_seconds || 3600)
        setOneoffAt(d.oneoff_at ?? null)
        setTimezone(d.timezone || 'UTC')
        setCalendarId(d.calendar_id ?? '')
        setWatchOn(d.watch_enabled)
        setWatchLoc(d.watch_location_id ?? '')
        setWatchPath(d.watch_path ?? '')
        setWatchEvery(d.watch_interval_seconds || 60)
        setWfParams(
          Object.entries(d.parameters ?? {})
            .map(([k, v]) => `${k}=${v}`)
            .join('\n'),
        )
        const members = new Set<string>()
        for (const e of d.edges) {
          members.add(e.from_job_id)
          members.add(e.to_job_id)
        }
        setMemberIDs(members)
        setEdges(d.edges.map((e) => ({ key: e.id, from: e.from_job_id, to: e.to_job_id, condition: e.condition as Cond })))
      })
      .catch((e) => toast.error(String(e)))
  }, [workflowId])

  useEffect(() => {
    reload()
  }, [reload])

  const save = () => {
    setSaving(true)
    api
      .updateWorkflow(workflowId, {
        name: wfName,
        description: wfDesc,
        enabled: wfEnabled,
        parameters: parseParams(wfParams),
        schedule_type: schedType,
        cron_expr: cronExpr,
        interval_seconds: intervalSec,
        oneoff_at: schedType === 'oneoff' ? oneoffAt : null,
        timezone,
        calendar_id: calendarId || '00000000-0000-0000-0000-000000000000',
        watch_enabled: watchOn,
        watch_location_id: watchLoc || '00000000-0000-0000-0000-000000000000',
        watch_path: watchPath,
        watch_interval_seconds: watchEvery,
      })
      .then(() =>
        api.replaceWorkflowEdges(
          workflowId,
          edges.map((e) => ({ from_job_id: e.from, to_job_id: e.to, condition: e.condition })),
        ),
      )
      .then(() => {
        toast.success('Workflow saved')
        onClose()
      })
      .catch((e) => toast.error(String(e)))
      .finally(() => setSaving(false))
  }

  const addEdge = () => {
    if (!addFrom || !addTo || addFrom === addTo) return
    const dup = edges.some((e) => e.from === addFrom && e.to === addTo && e.condition === addCond)
    if (dup) {
      toast.error('That link already exists')
      return
    }
    setEdges((es) => [...es, { key: `new-${Date.now()}`, from: addFrom, to: addTo, condition: addCond }])
    setMemberIDs((s) => new Set([...s, addFrom, addTo]))
    setAddFrom('')
    setAddTo('')
  }

  const removeEdge = (key: string) => setEdges((es) => es.filter((e) => e.key !== key))

  // Lay out members in depth columns (first jobs on the left).
  const lanes = useMemo(() => {
    const members = [...memberIDs].map((id) => jobs.find((j) => j.id === id)).filter(Boolean) as Job[]
    const out = new Map<string, number>()
    const adj = new Map<string, string[]>()
    const indeg = new Map<string, number>()
    for (const j of members) {
      out.set(j.id, 0)
      indeg.set(j.id, 0)
    }
    for (const e of edges) {
      adj.set(e.from, [...(adj.get(e.from) ?? []), e.to])
      indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1)
    }
    const queue = members.filter((j) => (indeg.get(j.id) ?? 0) === 0).map((j) => j.id)
    let guard = 0
    while (queue.length && guard < 1000) {
      guard++
      const id = queue.shift()!
      for (const nxt of adj.get(id) ?? []) {
        out.set(nxt, Math.max(out.get(nxt) ?? 0, (out.get(id) ?? 0) + 1))
        indeg.set(nxt, (indeg.get(nxt) ?? 0) - 1)
        if (indeg.get(nxt) === 0) queue.push(nxt)
      }
    }
    const byLane = new Map<number, Job[]>()
    for (const j of members) {
      const l = out.get(j.id) ?? 0
      byLane.set(l, [...(byLane.get(l) ?? []), j])
    }
    return [...byLane.entries()].sort((a, b) => a[0] - b[0])
  }, [memberIDs, edges, jobs])

  const freeJobs = jobs.filter((j) => !memberIDs.has(j.id))

  return (
    <Drawer
      open
      onClose={onClose}
      width="max-w-6xl"
      title={wfName || 'Workflow'}
      sub={wfEnabled ? 'On' : 'Paused'}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <WriteGate>
            <Button variant="primary" onClick={save} disabled={!wfName.trim() || saving}>
              {saving ? 'Saving…' : 'Save workflow'}
            </Button>
          </WriteGate>
        </>
      }
    >
      <div className="grid min-h-full gap-6 lg:grid-cols-[300px_minmax(0,1fr)]">
        <div className="space-y-5">
          <Field label="Name">
            <Input value={wfName} onChange={(e) => setWfName(e.target.value)} placeholder="nightly-etl" />
          </Field>
          <Field label="Description">
            <Input value={wfDesc} onChange={(e) => setWfDesc(e.target.value)} placeholder="Optional" />
          </Field>
          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-sm font-semibold text-ink">On</div>
              <div className="text-[13px] text-faint">A paused workflow doesn’t start its next jobs.</div>
            </div>
            <Switch label="Workflow on" checked={wfEnabled} onChange={setWfEnabled} />
          </div>
          <Field label="Parameters" hint="One NAME=value per line. Added to each job’s environment when the workflow runs.">
            <Textarea
              value={wfParams}
              onChange={(e) => setWfParams(e.target.value)}
              placeholder={'REGION=us-east\nENV=prod'}
              rows={4}
              spellCheck={false}
              className="font-mono text-[13px]"
            />
          </Field>

          <section aria-labelledby="wf-schedule" className="space-y-3 rounded-xl border border-line-soft bg-bg/50 p-4">
            <div id="wf-schedule" className="text-sm font-semibold text-ink">
              Schedule
            </div>
            <p className="text-[12px] text-faint">When the workflow fires, every first job starts under one execution. Give those jobs the Manual schedule type.</p>
            <Tabs
              label="Workflow schedule"
              value={schedType}
              onChange={(v) => setSchedType(v as ScheduleType)}
              tabs={[
                { value: 'manual', label: 'None' },
                { value: 'cron', label: 'Cron' },
                { value: 'interval', label: 'Interval' },
                { value: 'oneoff', label: 'Once' },
              ]}
            />
            {schedType === 'cron' && (
              <Field label="Cron expression" hint="Minute, hour, day of month, month, day of week.">
                <Input className="font-mono" value={cronExpr} onChange={(e) => setCronExpr(e.target.value)} />
              </Field>
            )}
            {schedType === 'interval' && (
              <Field label="Every (seconds)" hint="3600 is hourly, 86400 is daily.">
                <Input type="number" min={5} className="font-mono" value={intervalSec} onChange={(e) => setIntervalSec(Math.max(0, Number(e.target.value)))} />
              </Field>
            )}
            {schedType === 'oneoff' && (
              <Field label="Run at">
                <Input
                  type="datetime-local"
                  className="font-mono"
                  value={oneoffAt ? toLocalInput(oneoffAt) : ''}
                  onChange={(e) => setOneoffAt(e.target.value ? new Date(e.target.value).toISOString() : null)}
                />
              </Field>
            )}
            {schedType !== 'manual' && (
              <>
                <Field label="Time zone">
                  <Input list="wf-tz-list" value={timezone} onChange={(e) => setTimezone(e.target.value)} className="font-mono" />
                </Field>
                <datalist id="wf-tz-list">
                  {TZ_LIST.map((tz) => (
                    <option key={tz} value={tz} />
                  ))}
                </datalist>
                <Field label="Skip calendar" hint="Occurrences on these dates are skipped.">
                    <Select value={calendarId} onChange={(e) => setCalendarId(e.target.value)}>
                      <option value="">None</option>
                      {calendars.map((c) => (
                        <option key={c.id} value={c.id}>
                          {c.name}
                        </option>
                      ))}
                    </Select>
                  </Field>
              </>
            )}
          </section>

          <section aria-labelledby="wf-trigger" className="space-y-3 rounded-xl border border-line-soft bg-bg/50 p-4">
            <div className="flex items-center justify-between gap-4">
              <div>
                <div id="wf-trigger" className="text-sm font-semibold text-ink">
                  Also run when files appear
                </div>
                <div className="text-[12px] text-faint">Watches a storage location; the first jobs get the matched files.</div>
              </div>
              <Switch label="File trigger" checked={watchOn} onChange={setWatchOn} />
            </div>
            {watchOn && (
              <>
                <Field label="Storage location">
                  <Select value={watchLoc} onChange={(e) => setWatchLoc(e.target.value)}>
                    <option value="">Choose a location…</option>
                    {locations.map((l) => (
                      <option key={l.id} value={l.id}>
                        {l.name}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label="Path or glob" hint="Relative to the location, e.g. incoming/*.csv">
                  <Input className="font-mono" value={watchPath} onChange={(e) => setWatchPath(e.target.value)} placeholder="incoming/*.csv" />
                </Field>
                <Field label="Check every (seconds)" hint="At least 10.">
                  <Input type="number" min={10} className="font-mono" value={watchEvery} onChange={(e) => setWatchEvery(Math.max(0, Number(e.target.value)))} />
                </Field>
              </>
            )}
          </section>

          <section aria-labelledby="add-link" className="space-y-3 rounded-xl border border-line-soft bg-bg/50 p-4">
            <h3 id="add-link" className="text-sm font-semibold text-ink">
              Add a link
            </h3>
            <Select aria-label="When this job finishes" value={addFrom} onChange={(e) => setAddFrom(e.target.value)}>
              <option value="">When this job finishes…</option>
              {[...memberIDs].map((id) => (
                <option key={id} value={id}>
                  {jobName(id)}
                </option>
              ))}
              {freeJobs.map((j) => (
                <option key={j.id} value={j.id}>
                  {j.name}
                </option>
              ))}
            </Select>
            <div role="group" aria-label="Condition" className="flex flex-wrap gap-1.5">
              {(['success', 'failure', 'always'] as Cond[]).map((c) => (
                <button
                  key={c}
                  type="button"
                  aria-pressed={addCond === c}
                  onClick={() => setAddCond(c)}
                  className={cn(
                    'inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-[13px] font-semibold transition-colors cursor-pointer',
                    addCond === c
                      ? 'border-ink bg-ink text-bg'
                      : 'border-input/60 bg-panel text-ink hover:bg-raised',
                  )}
                >
                  <span className={cn('h-2 w-2', condDot[c])} aria-hidden="true" />
                  {condLabel[c]}
                </button>
              ))}
            </div>
            <Select aria-label="Start this job" value={addTo} onChange={(e) => setAddTo(e.target.value)}>
              <option value="">…start this job</option>
              {[...memberIDs]
                .filter((id) => id !== addFrom)
                .map((id) => (
                  <option key={id} value={id}>
                    {jobName(id)}
                  </option>
                ))}
              {freeJobs
                .filter((j) => j.id !== addFrom)
                .map((j) => (
                  <option key={j.id} value={j.id}>
                    {j.name}
                  </option>
                ))}
            </Select>
            <Button variant="secondary" className="w-full" disabled={!addFrom || !addTo || addFrom === addTo} onClick={addEdge}>
              <Plus size={14} /> Add link
            </Button>
          </section>

          <section aria-labelledby="links">
            <h3 id="links" className="mb-2 text-sm font-semibold text-ink">
              Links ({edges.length})
            </h3>
            {edges.length === 0 ? (
              <p className="text-[13px] text-faint">No links yet. Add one above.</p>
            ) : (
              <ul className="divide-y divide-line-soft rounded-xl border border-line-soft">
                {edges.map((e) => (
                  <li key={e.key} className="flex items-center gap-2 px-3 py-2">
                    <span className={cn('h-2 w-2 shrink-0', condDot[e.condition])} aria-hidden="true" />
                    <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-dim">
                      {jobName(e.from)} <ArrowRight size={11} className="inline" aria-hidden="true" /> {jobName(e.to)}
                      <span className="sr-only"> ({condLabel[e.condition]})</span>
                    </span>
                    <span className="text-xs text-faint" aria-hidden="true">
                      {condLabel[e.condition]}
                    </span>
                    <Button size="icon" variant="ghost" className="h-7 w-7" aria-label={`Remove link ${jobName(e.from)} to ${jobName(e.to)}`} onClick={() => removeEdge(e.key)}>
                      <Trash2 size={13} className="text-danger" />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>

        <div className="min-w-0 overflow-auto rounded-xl bg-raised/60 p-5">
          {memberIDs.size === 0 ? (
            <div className="flex h-full min-h-60 items-center justify-center text-center">
              <div>
                <GitBranch size={26} className="mx-auto mb-2 text-faint" aria-hidden="true" />
                <div className="text-sm font-semibold text-dim">Add a link to place jobs here</div>
                <div className="mt-1 text-[13px] text-faint">First jobs on the left, the jobs they start to the right.</div>
              </div>
            </div>
          ) : (
            <div className="flex items-start gap-8">
              {lanes.map(([lane, laneJobs]) => (
                <div key={lane} className="flex flex-col gap-3">
                  <div className="text-xs font-semibold text-faint">{lane === 0 ? 'Starts with' : `Step ${lane + 1}`}</div>
                  {laneJobs.map((j) => {
                    const outEdges = edges.filter((e) => e.from === j.id)
                    return (
                      <div key={j.id} className="w-56 rounded-xl border border-line bg-panel p-3.5">
                        <div className="truncate text-[13px] font-semibold text-ink">{j.name}</div>
                        <div className="mt-0.5 text-xs text-faint">
                          {j.exec_mode} · {targetLabel(j.target_type)}
                        </div>
                        {outEdges.map((e) => (
                          <div key={e.key} className="mt-2 flex items-center gap-1.5 text-xs">
                            <span className={cn('h-2 w-2 shrink-0', condDot[e.condition])} aria-hidden="true" />
                            <span className="text-faint">{condLabel[e.condition]}</span>
                            <ArrowRight size={11} className="text-faint" aria-hidden="true" />
                            <span className="truncate text-dim">{jobName(e.to)}</span>
                          </div>
                        ))}
                      </div>
                    )
                  })}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </Drawer>
  )
}

function parseParams(raw: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of raw.split('\n')) {
    const t = line.trim()
    if (!t || t.startsWith('#')) continue
    const i = t.indexOf('=')
    if (i <= 0) continue
    out[t.slice(0, i).trim()] = t.slice(i + 1).trim()
  }
  return out
}

function toLocalInput(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}
