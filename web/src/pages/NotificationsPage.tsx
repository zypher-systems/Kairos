import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { AlertTriangle, Bell, Globe, Hash, Mail, MessageSquare, Plus } from 'lucide-react'
import { api, type DeadLetter } from '@/lib/api'
import { Button, Input, Field, Select, Switch, Dialog, EmptyState, QueryError, RowMenu, MenuAction } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { cn, timeAgo } from '@/lib/utils'
import type { ChannelType, NotificationChannel } from '@/lib/types'
import { WriteGate, useAuth } from '@/lib/auth'

const typeMeta: Record<ChannelType, { icon: typeof Globe; label: string }> = {
  webhook: { icon: Globe, label: 'Webhook' },
  discord: { icon: MessageSquare, label: 'Discord' },
  slack: { icon: Hash, label: 'Slack' },
  smtp: { icon: Mail, label: 'Email' },
}

function summary(ch: NotificationChannel): string {
  const c = ch.config
  switch (ch.type) {
    case 'webhook':
      return c.url || ''
    case 'discord':
    case 'slack':
      return c.webhook_url ? 'Webhook URL stored' : 'No webhook URL'
    case 'smtp':
      return `${c.from ?? ''} → ${c.to ?? ''} via ${c.host ?? ''}:${c.port || '587'}`
  }
}

function events(ch: NotificationChannel): string[] {
  const out: string[] = []
  if (ch.notify_on_failure) out.push('Failures')
  if (ch.notify_on_success) out.push('Successes')
  if (ch.notify_on_missed) out.push('Missed')
  if (ch.notify_on_late) out.push('Late')
  if (ch.notify_on_pending) out.push('Approvals')
  if (ch.notify_on_rejected) out.push('Rejected')
  return out
}

export default function NotificationsPage() {
  const confirm = useConfirm()
  const { canWrite } = useAuth()
  const [channels, setChannels] = useState<NotificationChannel[]>()
  const [editing, setEditing] = useState<NotificationChannel | null>(null)
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState<unknown>()

  const reload = useCallback(() => {
    setError(undefined)
    api
      .listChannels()
      .then((r) => setChannels(r.channels))
      .catch((e) => setError(e))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const toggle = (ch: NotificationChannel, v: boolean) => {
    api
      .updateChannel(ch.id, { ...ch, enabled: v })
      .then(() => {
        toast.success(v ? `${ch.name} is on` : `${ch.name} is off`)
        reload()
      })
      .catch((e) => toast.error(String(e)))
  }

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Alerts</h1>
          <p className="mt-1.5 max-w-2xl text-sm text-faint">
            Where Kairos tells you about failed runs. Every channel that is on gets them, unless a job picks specific channels in its settings.
          </p>
        </div>
        <WriteGate>
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus size={16} strokeWidth={2.4} /> Add a channel
          </Button>
        </WriteGate>
      </header>

      {error != null ? <QueryError error={error} /> : null}

      <div className="rounded-2xl border border-line bg-panel">
        {channels && channels.length === 0 && (
          <EmptyState
            icon={Bell}
            title="No channels yet"
            sub="A job that fails at 3am shouldn’t wait until morning. Send alerts to Slack, Discord, email or any webhook."
            action={
              <WriteGate>
                <Button variant="primary" onClick={() => setAdding(true)}>
                  Add a channel
                </Button>
              </WriteGate>
            }
          />
        )}
        <ul>
          {channels?.map((ch) => {
            const meta = typeMeta[ch.type]
            const Icon = meta?.icon ?? Bell
            return (
              <li key={ch.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line-soft px-5 py-4 last:border-b-0">
                <Switch label={`${ch.name} is on`} checked={ch.enabled} disabled={!canWrite} onChange={(v) => toggle(ch, v)} />
                <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-raised text-dim">
                  <Icon size={17} aria-hidden="true" />
                </span>
                <div className={cn('min-w-0 flex-1', !ch.enabled && 'opacity-60')}>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-semibold text-ink">{ch.name}</span>
                    <span className="text-[13px] text-faint">{meta?.label ?? ch.type}</span>
                    {!ch.enabled && <span className="text-[13px] font-semibold text-faint">· Off</span>}
                  </div>
                  <div className="mt-0.5 truncate text-[13px] text-faint">
                    <span className="font-mono">{summary(ch)}</span> · added {timeAgo(ch.created_at)}
                  </div>
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    {events(ch).map((e) => (
                      <span key={e} className="rounded-md bg-raised px-2 py-0.5 text-xs text-dim">
                        {e}
                      </span>
                    ))}
                  </div>
                </div>
                <WriteGate>
                  <div className="flex gap-2">
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() =>
                        api
                          .testChannel(ch.id)
                          .then((r) => (r.ok ? toast.success(`Test sent to ${ch.name}`) : toast.error(r.error ?? 'Test failed')))
                          .catch((e) => toast.error(String(e)))
                      }
                    >
                      Send a test
                    </Button>
                    <RowMenu label={`Actions for ${ch.name}`}>
                      <MenuAction onSelect={() => setEditing(ch)}>Edit</MenuAction>
                      <MenuAction danger onSelect={() =>
                          confirm({
                            title: `Delete channel ${ch.name}?`,
                            body: 'Alerts routed to this channel stop being sent.',
                            confirmLabel: 'Delete channel',
                            destructive: true,
                          }).then(
                            (ok) =>
                              void (ok &&
                                api
                                  .deleteChannel(ch.id)
                                  .then(() => {
                                    toast.success(`${ch.name} deleted`)
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
            )
          })}
        </ul>
      </div>

      {adding && <ChannelDialog onDone={reload} onClose={() => setAdding(false)} />}
      {editing && (
        <ChannelDialog
          channel={editing}
          onDone={() => {
            setEditing(null)
            reload()
          }}
          onClose={() => setEditing(null)}
        />
      )}
      <DeadLetters channels={channels ?? []} />
    </div>
  )
}

interface ChannelForm {
  name: string
  type: ChannelType
  enabled: boolean
  notify_on_failure: boolean
  notify_on_success: boolean
  notify_on_missed: boolean
  notify_on_late: boolean
  notify_on_pending: boolean
  notify_on_rejected: boolean
  url: string
  secret: string
  webhook_url: string
  host: string
  port: string
  username: string
  password: string
  from: string
  to: string
  tls: string
}

const blank: ChannelForm = {
  name: '',
  type: 'webhook',
  enabled: true,
  notify_on_failure: true,
  notify_on_success: false,
  notify_on_missed: true,
  notify_on_late: true,
  notify_on_pending: true,
  notify_on_rejected: true,
  url: '',
  secret: '',
  webhook_url: '',
  host: '',
  port: '587',
  username: '',
  password: '',
  from: '',
  to: '',
  tls: 'starttls',
}


// DeadLetters lists notifications that exhausted their delivery attempts.
// They stay until an operator retries or dismisses them, so a broken
// webhook is visible here instead of in a log line that scrolled away.
function DeadLetters({ channels }: { channels: NotificationChannel[] }) {
  const [rows, setRows] = useState<DeadLetter[]>([])
  const reload = useCallback(() => {
    api
      .listDeadLetters()
      .then((r) => setRows(r.dead_letters ?? []))
      .catch(() => setRows([]))
  }, [])
  useEffect(() => {
    reload()
  }, [reload])
  if (rows.length === 0) return null
  const channelName = (id: string) => channels.find((c) => c.id === id)?.name ?? 'deleted channel'
  return (
    <section className="rounded-2xl border border-danger/30 bg-panel">
      <div className="flex items-center gap-2 border-b border-line-soft px-5 py-3">
        <AlertTriangle size={16} className="text-danger" />
        <h2 className="text-sm font-semibold text-ink">Undelivered</h2>
        <span className="text-xs text-faint">
          {rows.length} notification{rows.length === 1 ? '' : 's'} gave up after repeated failures.
        </span>
      </div>
      <ul className="divide-y divide-line-soft">
        {rows.map((d) => (
          <li key={d.id} className="flex flex-wrap items-center gap-3 px-5 py-3">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-ink">
                {d.payload.event ?? 'event'} · {d.payload.job_name ?? '—'} → {channelName(d.channel_id)}
              </div>
              <div className="truncate font-mono text-xs text-faint" title={d.last_error}>
                {d.attempt} attempts · {d.dead_at ? timeAgo(d.dead_at) : ''} · {d.last_error}
              </div>
            </div>
            <WriteGate>
              <Button
                size="sm"
                variant="secondary"
                onClick={() =>
                  api
                    .retryDeadLetter(d.id)
                    .then(() => {
                      toast.success('Queued for delivery')
                      reload()
                    })
                    .catch((e) => toast.error(String(e)))
                }
              >
                Retry
              </Button>
              <Button
                size="sm"
                variant="danger-ghost"
                onClick={() =>
                  api
                    .dismissDeadLetter(d.id)
                    .then(reload)
                    .catch((e) => toast.error(String(e)))
                }
              >
                Dismiss
              </Button>
            </WriteGate>
          </li>
        ))}
      </ul>
    </section>
  )
}

function ChannelDialog({ channel, onDone, onClose }: { channel?: NotificationChannel; onDone: () => void; onClose?: () => void }) {
  const isEdit = !!channel
  const [open, setOpen] = useState(true)
  const [busy, setBusy] = useState(false)
  const [form, setForm] = useState<ChannelForm>(
    channel
      ? {
          ...blank,
          name: channel.name,
          type: channel.type,
          enabled: channel.enabled,
          notify_on_failure: channel.notify_on_failure,
          notify_on_success: channel.notify_on_success,
          notify_on_missed: channel.notify_on_missed ?? true,
          notify_on_late: channel.notify_on_late ?? true,
          notify_on_pending: channel.notify_on_pending ?? true,
          notify_on_rejected: channel.notify_on_rejected ?? true,
          url: channel.config.url ?? '',
          host: channel.config.host ?? '',
          port: channel.config.port ?? '587',
          username: channel.config.username ?? '',
          from: channel.config.from ?? '',
          to: channel.config.to ?? '',
          tls: channel.config.tls ?? 'starttls',
          // secrets stay blank — masked values are never edited directly
        }
      : blank,
  )

  const close = () => {
    setOpen(false)
    onClose?.()
  }

  const valid =
    form.name.trim() !== '' &&
    ((form.type === 'webhook' && form.url.trim() !== '') ||
      ((form.type === 'discord' || form.type === 'slack') && (isEdit || form.webhook_url.trim() !== '')) ||
      (form.type === 'smtp' && form.host.trim() !== '' && form.from.trim() !== '' && form.to.trim() !== ''))

  const save = () => {
    setBusy(true)
    const config: Record<string, string> = {}
    if (form.type === 'webhook') {
      config.url = form.url
      if (form.secret) config.secret = form.secret
    } else if (form.type === 'discord' || form.type === 'slack') {
      config.webhook_url = form.webhook_url
    } else {
      config.host = form.host
      config.port = form.port
      config.from = form.from
      config.to = form.to
      config.tls = form.tls
      if (form.username) config.username = form.username
      if (form.password) config.password = form.password
    }
    const payload = {
      name: form.name,
      type: form.type,
      enabled: form.enabled,
      notify_on_failure: form.notify_on_failure,
      notify_on_success: form.notify_on_success,
      notify_on_missed: form.notify_on_missed,
      notify_on_late: form.notify_on_late,
      notify_on_pending: form.notify_on_pending,
      notify_on_rejected: form.notify_on_rejected,
      config,
    }
    const req = isEdit ? api.updateChannel(channel!.id, payload) : api.createChannel(payload)
    req
      .then(() => {
        toast.success(isEdit ? 'Channel updated' : `Channel "${form.name}" created`)
        onDone()
        close()
      })
      .catch((e) => toast.error(String(e)))
      .finally(() => setBusy(false))
  }

  const set = <K extends keyof ChannelForm>(k: K, v: ChannelForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  return (
    <>
      <Dialog
        open={open}
        onClose={close}
        title={isEdit ? `Edit ${channel!.name}` : 'Add an alert channel'}
        footer={
          <>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button variant="primary" onClick={save} disabled={!valid || busy}>
              {busy ? 'Saving…' : isEdit ? 'Save changes' : 'Create channel'}
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Name">
              <Input value={form.name} onChange={(e) => set('name', e.target.value)} placeholder="ops-oncall" autoFocus />
            </Field>
            <Field label="Type">
              <Select value={form.type} onChange={(e) => set('type', e.target.value as ChannelType)}>
                <option value="webhook">Webhook</option>
                <option value="discord">Discord</option>
                <option value="slack">Slack</option>
                <option value="smtp">Email</option>
              </Select>
            </Field>
          </div>

          {form.type === 'webhook' && (
            <>
              <Field label="URL" hint="Kairos POSTs each alert as JSON.">
                <Input className="font-mono" value={form.url} onChange={(e) => set('url', e.target.value)} placeholder="https://ops.example.com/kairos" />
              </Field>
              <Field label="Signing secret" hint="Optional. Signs each request so you can check it came from Kairos (X-Kairos-Signature header).">
                <Input type="password" value={form.secret} onChange={(e) => set('secret', e.target.value)} placeholder={isEdit ? 'Stored; leave empty to keep' : 'Optional'} />
              </Field>
            </>
          )}

          {(form.type === 'discord' || form.type === 'slack') && (
            <Field
              label="Webhook URL"
              hint={isEdit ? 'Stored encrypted; leave empty to keep the current URL.' : `From your ${form.type === 'slack' ? 'Slack' : 'Discord'} channel settings. Stored encrypted.`}
            >
              <Input
                type="password"
                className="font-mono"
                value={form.webhook_url}
                onChange={(e) => set('webhook_url', e.target.value)}
                placeholder={isEdit ? 'Stored; leave empty to keep' : `https://${form.type === 'discord' ? 'discord.com/api/webhooks/…' : 'hooks.slack.com/services/…'}`}
              />
            </Field>
          )}

          {form.type === 'smtp' && (
            <>
              <div className="grid grid-cols-[1.5fr_90px] gap-3">
                <Field label="Mail server">
                  <Input className="font-mono" value={form.host} onChange={(e) => set('host', e.target.value)} placeholder="smtp.example.com" />
                </Field>
                <Field label="Port">
                  <Input className="font-mono" value={form.port} onChange={(e) => set('port', e.target.value)} />
                </Field>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Username" hint="Optional.">
                  <Input value={form.username} onChange={(e) => set('username', e.target.value)} />
                </Field>
                <Field label="Password" hint={isEdit ? 'Leave empty to keep.' : 'Optional.'}>
                  <Input type="password" value={form.password} onChange={(e) => set('password', e.target.value)} />
                </Field>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <Field label="From">
                  <Input className="font-mono" value={form.from} onChange={(e) => set('from', e.target.value)} placeholder="kairos@example.com" />
                </Field>
                <Field label="To" hint="Separate addresses with commas.">
                  <Input className="font-mono" value={form.to} onChange={(e) => set('to', e.target.value)} placeholder="ops@example.com" />
                </Field>
              </div>
              <Field label="Encryption">
                <Select value={form.tls} onChange={(e) => set('tls', e.target.value)}>
                  <option value="starttls">STARTTLS (587)</option>
                  <option value="ssl">SSL/TLS (465)</option>
                  <option value="none">None (25, local relay only)</option>
                </Select>
              </Field>
            </>
          )}

          <div className="mt-3 space-y-3.5 border-t border-line-soft pt-4">
            <div className="text-sm font-semibold text-ink">Send an alert when</div>
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-medium text-ink">A run fails</div>
                <div className="text-[13px] text-faint">Includes canceled and timed-out runs.</div>
              </div>
              <Switch label="A run fails" checked={form.notify_on_failure} onChange={(v) => set('notify_on_failure', v)} />
            </div>
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-medium text-ink">A run succeeds</div>
                <div className="text-[13px] text-faint">Usually noisy; best on a channel for a few critical jobs.</div>
              </div>
              <Switch label="A run succeeds" checked={form.notify_on_success} onChange={(v) => set('notify_on_success', v)} />
            </div>
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-medium text-ink">A run doesn’t start on time</div>
                <div className="text-[13px] text-faint">Needs missed-run alerts set on the job (Pro).</div>
              </div>
              <Switch label="A run doesn’t start on time" checked={form.notify_on_missed} onChange={(v) => set('notify_on_missed', v)} />
            </div>
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-medium text-ink">A run takes too long</div>
                <div className="text-[13px] text-faint">Needs a late-run limit set on the job (Pro). Only alerts; it doesn’t stop the run.</div>
              </div>
              <Switch label="A run takes too long" checked={form.notify_on_late} onChange={(v) => set('notify_on_late', v)} />
            </div>
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-medium text-ink">A run is waiting for approval</div>
                <div className="text-[13px] text-faint">For jobs that require approval (Pro).</div>
              </div>
              <Switch label="A run is waiting for approval" checked={form.notify_on_pending} onChange={(v) => set('notify_on_pending', v)} />
            </div>
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-medium text-ink">A run is rejected or not approved in time</div>
              </div>
              <Switch label="A run is rejected or not approved in time" checked={form.notify_on_rejected} onChange={(v) => set('notify_on_rejected', v)} />
            </div>
            <div className="flex items-center justify-between border-t border-line-soft pt-3.5">
              <div>
                <div className="text-sm font-medium text-ink">Channel is on</div>
                <div className="text-[13px] text-faint">A channel that’s off receives nothing.</div>
              </div>
              <Switch label="Channel is on" checked={form.enabled} onChange={(v) => set('enabled', v)} />
            </div>
          </div>
        </div>
      </Dialog>
    </>
  )
}

