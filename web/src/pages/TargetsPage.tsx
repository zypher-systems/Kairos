import { useEffect, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { AlertTriangle, ArrowLeft, Box, Check, Copy, Cpu, KeyRound, Plus, Server, Terminal } from 'lucide-react'
import { api } from '@/lib/api'
import { Button, Input, Field, Select, Textarea, Switch, Dialog, EmptyState, Tabs, RowMenu, MenuAction } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { useAgents, useSSHHosts } from '@/hooks/queries'
import { cn, timeAgo } from '@/lib/utils'
import type { SSHHost, SSHKey } from '@/lib/types'
import { WriteGate } from '@/lib/auth'

export default function TargetsPage() {
  const confirm = useConfirm()
  const qc = useQueryClient()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') === 'ssh' ? 'ssh' : 'agents'
  const { data: agents } = useAgents()
  const { data: hosts } = useSSHHosts()
  const [editing, setEditing] = useState<SSHHost | null>(null)
  const [adding, setAdding] = useState(false)
  const [newHost, setNewHost] = useState(false)

  const reload = () => {
    qc.invalidateQueries({ queryKey: ['agents'] })
    qc.invalidateQueries({ queryKey: ['ssh-hosts'] })
  }
  const online = agents?.filter((a) => a.online).length ?? 0

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Hosts &amp; agents</h1>
          <p className="mt-1.5 text-sm text-faint">
            The machines your jobs run on.
            {agents && ` ${online} of ${agents.length} ${agents.length === 1 ? 'agent' : 'agents'} online.`}
          </p>
        </div>
        <WriteGate>
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus size={16} strokeWidth={2.4} /> Add a host
          </Button>
        </WriteGate>
      </header>

      <Tabs
        label="Host type"
        value={tab}
        onChange={(v) => setParams(v === 'ssh' ? { tab: 'ssh' } : {}, { replace: true })}
        tabs={[
          { value: 'agents', label: `Agents${agents ? ` · ${agents.length}` : ''}` },
          { value: 'ssh', label: `SSH hosts${hosts ? ` · ${hosts.length}` : ''}` },
        ]}
      />

      {tab === 'agents' && (
        <div className="rounded-2xl border border-line bg-panel">
          {agents && agents.length === 0 && (
            <EmptyState
              icon={Server}
              title="No agents yet"
              sub="An agent is a small program on your machine that picks up jobs from Kairos. It only makes outbound connections."
              action={
                <WriteGate>
                  <Button variant="primary" onClick={() => setAdding(true)}>
                    Add a host
                  </Button>
                </WriteGate>
              }
            />
          )}
          <ul>
            {agents?.map((ag) => (
              <li key={ag.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line-soft px-5 py-4 last:border-b-0">
                <span
                  className={cn('h-2.5 w-2.5 shrink-0', ag.online ? 'rounded-full bg-ok' : 'rounded-[2px] bg-danger')}
                  aria-hidden="true"
                />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[15px] font-semibold text-ink">{ag.name}</span>
                    <span className={cn('text-[13px] font-semibold', ag.online ? 'text-ok' : 'text-danger')}>{ag.online ? 'Online' : 'Offline'}</span>
                  </div>
                  <div className="mt-0.5 text-[13px] text-faint">
                    {ag.hostname || 'Hostname not reported yet'} · {ag.version ? `v${ag.version}` : 'version unknown'} · last seen{' '}
                    {ag.last_heartbeat_at ? timeAgo(ag.last_heartbeat_at) : 'never'}
                  </div>
                </div>
                <div className="flex max-w-md flex-wrap gap-1.5">
                  {Object.entries(ag.labels).map(([k, v]) => (
                    <span key={k} className="rounded-md bg-raised px-2 py-0.5 font-mono text-xs text-dim">
                      {k}={v}
                    </span>
                  ))}
                  {Object.entries(ag.capabilities).map(([k, v]) => (
                    <span key={k} className="inline-flex items-center gap-1 rounded-md bg-cyan-soft px-2 py-0.5 font-mono text-xs text-cyan-ink">
                      {k === 'docker' ? <Box size={11} aria-hidden="true" /> : <Cpu size={11} aria-hidden="true" />} {k} {v}
                    </span>
                  ))}
                </div>
                <WriteGate>
                  <RowMenu label={`Actions for ${ag.name}`}>
                    <MenuAction danger onSelect={() =>
                        confirm({
                          title: `Remove agent ${ag.name}?`,
                          body: 'Jobs that target this agent stop running until they get another target, and its token stops working.',
                          confirmLabel: 'Remove agent',
                          destructive: true,
                        }).then(
                          (ok) =>
                            void (ok &&
                              api
                                .deleteAgent(ag.id)
                                .then(() => {
                                  toast.success('Agent removed')
                                  reload()
                                })
                                .catch((e) => toast.error(String(e)))),
                        )
                      }
                    >
                      Remove…
                    </MenuAction>
                  </RowMenu>
                </WriteGate>
              </li>
            ))}
          </ul>
        </div>
      )}

      {tab === 'ssh' && (
        <div className="rounded-2xl border border-line bg-panel">
          {hosts && hosts.length === 0 && (
            <EmptyState
              icon={KeyRound}
              title="No SSH hosts yet"
              sub="Kairos can run jobs over SSH without installing anything on the machine. Credentials are stored encrypted."
              action={
                <WriteGate>
                  <Button variant="primary" onClick={() => setNewHost(true)}>
                    Add an SSH host
                  </Button>
                </WriteGate>
              }
            />
          )}
          <ul>
            {hosts?.map((h) => (
              <li key={h.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line-soft px-5 py-4 last:border-b-0">
                <Terminal size={18} className="shrink-0 text-faint" aria-hidden="true" />
                <div className="min-w-0 flex-1">
                  <div className="text-[15px] font-semibold text-ink">{h.name}</div>
                  <div className="mt-0.5 text-[13px] text-faint">
                    <span className="font-mono">
                      {h.username}@{h.host}:{h.port}
                    </span>{' '}
                    · {h.auth_type === 'managed_key' ? `key ${h.ssh_key_name || ''}` : h.auth_type === 'password' ? 'password' : 'private key'} ·{' '}
                    {h.host_key_fingerprint ? 'host key saved' : 'host key saved on first connect'}
                  </div>
                </div>
                <WriteGate>
                  <div className="flex gap-2">
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() =>
                        api
                          .testSSHHost(h.id)
                          .then((r) => (r.ok ? toast.success(`Connected to ${h.name}`) : toast.error(r.error ?? 'Could not connect')))
                          .catch((e) => toast.error(String(e)))
                      }
                    >
                      Test
                    </Button>
                    <RowMenu label={`Actions for ${h.name}`}>
                      <MenuAction onSelect={() => setEditing(h)}>Edit</MenuAction>
                      <MenuAction danger onSelect={() =>
                          confirm({
                            title: `Remove host ${h.name}?`,
                            body: 'Its stored credentials are deleted. Jobs that run on this host fail until they are moved.',
                            confirmLabel: 'Remove host',
                            destructive: true,
                          }).then(
                            (ok) =>
                              void (ok &&
                                api
                                  .deleteSSHHost(h.id)
                                  .then(() => {
                                    toast.success('Host removed')
                                    reload()
                                  })
                                  .catch((e) => toast.error(String(e)))),
                          )
                        }
                      >
                        Remove…
                      </MenuAction>
                    </RowMenu>
                  </div>
                </WriteGate>
              </li>
            ))}
          </ul>
        </div>
      )}

      {adding && (
        <AddHostWizard
          hosts={hosts ?? []}
          onClose={() => setAdding(false)}
          onDone={reload}
          onSSHOnly={() => {
            setAdding(false)
            setNewHost(true)
          }}
        />
      )}
      {newHost && (
        <SSHHostDialog
          onDone={() => {
            reload()
            setParams({ tab: 'ssh' }, { replace: true })
          }}
          onClose={() => setNewHost(false)}
        />
      )}
      {editing && (
        <SSHHostDialog
          host={editing}
          onDone={() => {
            setEditing(null)
            reload()
          }}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  )
}

type Path = 'choose' | 'command' | 'ssh-install'

// AddHostWizard walks through the three ways to give Kairos a machine.
function AddHostWizard({ hosts, onClose, onDone, onSSHOnly }: { hosts: SSHHost[]; onClose: () => void; onDone: () => void; onSSHOnly: () => void }) {
  const [path, setPath] = useState<Path>('choose')
  const [serverURL, setServerURL] = useState<{ server_url: string; from_config: boolean } | null>(null)
  useEffect(() => {
    api.agentServerURL().then(setServerURL).catch(() => undefined)
  }, [])

  const title = path === 'choose' ? 'Add a host' : path === 'command' ? 'Install an agent' : 'Install an agent over SSH'
  return (
    <Dialog open onClose={onClose} title={title} width="max-w-2xl">
      {path !== 'choose' && (
        <button type="button" onClick={() => setPath('choose')} className="mb-4 inline-flex items-center gap-1.5 text-[13px] font-semibold text-dim hover:text-ink">
          <ArrowLeft size={14} aria-hidden="true" /> Other ways to connect
        </button>
      )}
      {path === 'choose' && (
        <div className="grid gap-3">
          <Choice
            recommended
            icon={Terminal}
            title="Run one command on the machine"
            body="Installs the Kairos agent. It connects out to Kairos, so it works behind NAT and firewalls."
            onClick={() => setPath('command')}
          />
          <Choice
            icon={Server}
            title="Let Kairos install the agent over SSH"
            body="Kairos signs in to a machine you’ve added as an SSH host and installs the agent for you."
            onClick={() => setPath('ssh-install')}
          />
          <Choice
            icon={KeyRound}
            title="Use SSH only"
            body="Nothing is installed. Kairos runs each job over SSH. Best for a few machines you can reach directly."
            onClick={onSSHOnly}
          />
        </div>
      )}
      {path !== 'choose' && serverURL && !serverURL.from_config && (
        <div className="mb-4 flex gap-3 rounded-xl bg-warn-soft px-4 py-3 text-[13px] text-warn">
          <AlertTriangle size={16} className="mt-0.5 shrink-0" aria-hidden="true" />
          <div>
            Agents will connect to <span className="font-mono font-semibold">{serverURL.server_url}</span>, which Kairos guessed. If your machines can’t
            reach that address, set <span className="font-mono">KAIROS_PUBLIC_URL</span> and restart Kairos.
          </div>
        </div>
      )}
      {path === 'command' && <CommandInstall serverURL={serverURL?.server_url ?? window.location.origin} onDone={onDone} onClose={onClose} />}
      {path === 'ssh-install' && <SSHInstall hosts={hosts} onDone={onDone} onClose={onClose} onAddHost={onSSHOnly} />}
    </Dialog>
  )
}

function Choice({ icon: Icon, title, body, onClick, recommended }: { icon: typeof Server; title: string; body: string; onClick: () => void; recommended?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex items-start gap-4 rounded-xl border border-line p-4 text-left transition-colors hover:border-accent hover:bg-accent-soft/40 cursor-pointer"
    >
      <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-raised text-dim">
        <Icon size={19} aria-hidden="true" />
      </span>
      <span>
        <span className="flex items-center gap-2 text-sm font-semibold text-ink">
          {title}
          {recommended && <span className="rounded-md bg-ok-soft px-1.5 py-px text-[11px] font-bold text-ok">Recommended</span>}
        </span>
        <span className="mt-0.5 block text-[13px] text-faint">{body}</span>
      </span>
    </button>
  )
}

function Step({ n, title, children }: { n: number; title: string; children: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-ink text-xs font-bold text-bg">{n}</span>
      <div className="min-w-0 flex-1">
        <div className="mb-2 text-sm font-semibold text-ink">{title}</div>
        {children}
      </div>
    </li>
  )
}

function CommandInstall({ serverURL, onDone, onClose }: { serverURL: string; onDone: () => void; onClose: () => void }) {
  const [name, setName] = useState('')
  const [labels, setLabels] = useState('')
  const [token, setToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const { data: agents } = useAgents()
  const connected = token && agents?.some((a) => a.name === (name || 'worker') && a.online)

  const create = () => {
    const lbl: Record<string, string> = {}
    for (const pair of labels.split(',')) {
      const [k, v] = pair.split('=').map((x) => x.trim())
      if (k && v) lbl[k] = v
    }
    api
      .createAgent(name.trim() || 'worker', lbl)
      .then((r) => {
        setToken(r.token)
        onDone()
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
  }

  const cmd = token ? `curl -fsSL ${serverURL}/api/v1/agent/install.sh | sh -s -- --server ${serverURL} --token ${token} --name ${name.trim() || 'worker'}` : ''

  return (
    <ol className="space-y-5">
      <Step n={1} title="Name the machine">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Agent name">
            <Input value={name} disabled={!!token} onChange={(e) => setName(e.target.value)} placeholder="db-01" autoFocus />
          </Field>
          <Field label="Labels" hint="Optional, for example role=web,env=prod">
            <Input value={labels} disabled={!!token} onChange={(e) => setLabels(e.target.value)} className="font-mono" placeholder="role=web" />
          </Field>
        </div>
        {!token && (
          <Button className="mt-3" variant="primary" disabled={!name.trim()} onClick={create}>
            Create install command
          </Button>
        )}
      </Step>
      <Step n={2} title="Run this on the machine as root">
        {token ? (
          <>
            <div className="relative rounded-xl bg-terminal p-4 pr-12 font-mono text-[12.5px] leading-relaxed break-all text-terminal-ink">
              {cmd}
              <button
                type="button"
                aria-label="Copy command"
                onClick={() => {
                  void navigator.clipboard.writeText(cmd)
                  setCopied(true)
                  toast.success('Command copied')
                }}
                className="absolute top-3 right-3 rounded-md p-1.5 text-terminal-dim hover:bg-white/10 hover:text-white"
              >
                {copied ? <Check size={15} /> : <Copy size={15} />}
              </button>
            </div>
            <p className="mt-2 text-[13px] text-faint">
              The command contains a secret token and is shown only once. It installs the agent as a systemd service (Linux, amd64 or arm64).
            </p>
            {serverURL.startsWith('http://') && (
              <p className="mt-2 flex gap-2 text-[13px] text-warn">
                <AlertTriangle size={15} className="mt-0.5 shrink-0" aria-hidden="true" />
                This address is plain HTTP, so the token travels unencrypted. Serve Kairos over HTTPS before connecting machines across networks you
                don’t control.
              </p>
            )}
          </>
        ) : (
          <p className="text-[13px] text-faint">The command appears here after step 1.</p>
        )}
      </Step>
      <Step n={3} title="Wait for it to connect">
        {!token ? (
          <p className="text-[13px] text-faint">This page updates when the agent checks in.</p>
        ) : connected ? (
          <div className="flex items-center justify-between gap-3 rounded-xl bg-ok-soft px-4 py-3 text-sm font-semibold text-ok">
            <span className="flex items-center gap-2">
              <Check size={16} /> {name || 'worker'} is online.
            </span>
            <Button size="sm" variant="primary" onClick={onClose}>
              Done
            </Button>
          </div>
        ) : (
          <p className="flex items-center gap-2 text-[13px] text-faint">
            <span className="pulse-dot h-2 w-2 rounded-full bg-cyan" aria-hidden="true" /> Waiting for {name || 'worker'}…
          </p>
        )}
      </Step>
    </ol>
  )
}

function SSHInstall({ hosts, onDone, onClose, onAddHost }: { hosts: SSHHost[]; onDone: () => void; onClose: () => void; onAddHost: () => void }) {
  const [hostID, setHostID] = useState('')
  const [name, setName] = useState('')
  const [labels, setLabels] = useState('')
  const [installing, setInstalling] = useState(false)

  const install = () => {
    setInstalling(true)
    api
      .pushInstallAgent(hostID, name, labels)
      .then((r) => {
        if (!r.ok) return void toast.error(r.error ?? 'Install failed')
        toast.success(`Installing. The agent registers as "${r.agent?.name ?? 'agent'}" in a moment.`)
        onDone()
        onClose()
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setInstalling(false))
  }

  if (hosts.length === 0) {
    return (
      <div className="rounded-xl bg-raised p-5 text-sm text-dim">
        Add the machine as an SSH host first, then come back here to install the agent on it.
        <div className="mt-3">
          <Button variant="primary" onClick={onAddHost}>
            Add an SSH host
          </Button>
        </div>
      </div>
    )
  }
  return (
    <div className="grid gap-4">
      <Field label="SSH host">
        <Select value={hostID} onChange={(e) => setHostID(e.target.value)}>
          <option value="">Choose a host…</option>
          {hosts.map((h) => (
            <option key={h.id} value={h.id}>
              {h.name} ({h.username}@{h.host})
            </option>
          ))}
        </Select>
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Agent name" hint="Defaults to the host’s name.">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="worker-1" />
        </Field>
        <Field label="Labels" hint="Optional, for example role=web,env=prod">
          <Input className="font-mono" value={labels} onChange={(e) => setLabels(e.target.value)} placeholder="role=web" />
        </Field>
      </div>
      <div className="flex justify-end gap-2 border-t border-line-soft pt-4">
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button variant="primary" disabled={!hostID || installing} onClick={install}>
          {installing ? 'Installing…' : 'Install agent'}
        </Button>
      </div>
    </div>
  )
}

interface HostForm {
  name: string
  host: string
  port: string
  username: string
  auth_type: string
  ssh_key_id: string
  private_key: string
  password: string
  verify_host_key: boolean
  forget_host_key: boolean
}

const blankHostForm: HostForm = {
  name: '',
  host: '',
  port: '22',
  username: 'root',
  auth_type: 'key',
  ssh_key_id: '',
  private_key: '',
  password: '',
  verify_host_key: true,
  forget_host_key: false,
}

// SSHHostDialog handles both create and edit. In edit mode it also carries
// the "provision key access" step: install a managed key's public half on
// the host via one-time password auth, flipping the host to that key.
function SSHHostDialog({ host, onDone, onClose }: { host?: SSHHost; onDone: () => void; onClose?: () => void }) {
  const isEdit = !!host
  const [open, setOpen] = useState(true)
  const [keys, setKeys] = useState<SSHKey[]>([])
  const [form, setForm] = useState<HostForm>(
    host
      ? {
          name: host.name,
          host: host.host,
          port: String(host.port),
          username: host.username,
          auth_type: host.auth_type,
          ssh_key_id: host.ssh_key_id ?? '',
          private_key: '',
          password: '',
          verify_host_key: host.verify_host_key,
          forget_host_key: false,
        }
      : blankHostForm,
  )
  const [pushKeyID, setPushKeyID] = useState('')
  const [pushPassword, setPushPassword] = useState('')
  const [pushing, setPushing] = useState(false)

  useEffect(() => {
    if (open) api.listSSHKeys().then((r) => setKeys(r.keys ?? [])).catch(() => undefined)
  }, [open])

  const close = () => {
    setOpen(false)
    onClose?.()
  }

  const authOK =
    form.auth_type === 'password' ||
    (form.auth_type === 'key' && (isEdit || form.private_key.trim() !== '')) ||
    (form.auth_type === 'managed_key' && form.ssh_key_id !== '')

  const save = () => {
    const payload = {
      ...form,
      port: Number(form.port),
      ssh_key_id: form.ssh_key_id || null,
    }
    const req = isEdit ? api.updateSSHHost(host!.id, payload) : api.createSSHHost(payload)
    req
      .then(() => {
        toast.success(isEdit ? 'Host updated' : 'SSH host added')
        onDone()
        close()
      })
      .catch((e) => toast.error(String(e)))
  }

  const push = () => {
    setPushing(true)
    api
      .pushSSHKey(pushKeyID, host!.id, pushPassword)
      .then((r) => {
        if (r.ok) {
          toast.success('Key installed. The host now uses it.')
          setPushKeyID('')
          setPushPassword('')
          onDone()
          close()
        } else {
          toast.error(r.error ?? 'push failed')
        }
      })
      .catch((e) => toast.error(String(e)))
      .finally(() => setPushing(false))
  }

  return (
    <>
      <Dialog
        open={open}
        onClose={close}
        title={isEdit ? `Edit ${host!.name}` : 'Add an SSH host'}
        footer={
          <>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button variant="primary" onClick={save} disabled={!form.name || !form.host || !authOK}>
              {isEdit ? 'Save changes' : 'Add host'}
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Name">
              <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="db-01" />
            </Field>
            <Field label="Username">
              <Input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
            </Field>
          </div>
          <div className="grid grid-cols-[1fr_90px] gap-3">
            <Field label="Host">
              <Input value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} placeholder="db1.example.com" />
            </Field>
            <Field label="Port">
              <Input className="font-mono" value={form.port} onChange={(e) => setForm({ ...form, port: e.target.value })} />
            </Field>
          </div>
          <Field label="Authentication">
            <Select value={form.auth_type} onChange={(e) => setForm({ ...form, auth_type: e.target.value })}>
              <option value="key">Paste a private key</option>
              <option value="managed_key">A key stored in Kairos</option>
              <option value="password">Password</option>
            </Select>
          </Field>
          {form.auth_type === 'key' && (
            <Field
              label="Private key"
              hint={isEdit ? 'Leave empty to keep the stored key.' : 'Stored encrypted. It is never shown again.'}
            >
              <Textarea
                rows={5}
                className="font-mono text-[12px]"
                value={form.private_key}
                onChange={(e) => setForm({ ...form, private_key: e.target.value })}
                placeholder={isEdit && host!.has_private_key ? 'Stored; leave empty to keep' : '-----BEGIN OPENSSH PRIVATE KEY-----'}
              />
            </Field>
          )}
          {form.auth_type === 'managed_key' && (
            <Field label="Key" hint="Create or import keys under SSH keys.">
              <Select value={form.ssh_key_id} onChange={(e) => setForm({ ...form, ssh_key_id: e.target.value })}>
                <option value="">Choose a key…</option>
                {keys.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.name}
                  </option>
                ))}
              </Select>
              {keys.length === 0 && <p className="mt-1.5 text-[12px] text-faint">No keys yet. Create one under SSH keys first.</p>}
            </Field>
          )}
          {form.auth_type === 'password' && (
            <Field
              label="Password"
              hint={isEdit ? 'Leave empty to keep the stored password.' : 'Stored encrypted. It is never shown again.'}
            >
              <Input
                type="password"
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                placeholder={isEdit && host!.has_password ? 'Stored; leave empty to keep' : ''}
              />
            </Field>
          )}
          <div className="flex items-center justify-between border-t border-line-soft pt-3">
            <div>
              <div className="text-sm font-semibold text-ink">Verify the host key</div>
              <div className="text-[13px] text-faint">Refuse to connect if the host’s key changes. Recommended.</div>
            </div>
            <Switch label="Verify the host key" checked={form.verify_host_key} onChange={(v) => setForm({ ...form, verify_host_key: v })} />
          </div>
          {isEdit && host!.host_key_fingerprint && (
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-semibold text-ink">Forget the saved host key</div>
                <div className="text-[13px] text-faint">Only if the machine was rebuilt. The next connection saves its new key.</div>
              </div>
              <Switch label="Forget the saved host key" checked={form.forget_host_key} onChange={(v) => setForm({ ...form, forget_host_key: v })} />
            </div>
          )}

          {isEdit && (
            <div className="mt-2 border-t border-line-soft pt-3">
              <div className="mb-1 text-sm font-semibold text-ink">Switch this host to a stored key</div>
              <p className="mb-3 text-[13px] text-faint">
                Kairos signs in once with the current password, installs the key, and uses the key from then on. The password is not stored.
              </p>
              <div className="grid gap-2.5">
                <Select aria-label="Key to install" value={pushKeyID} onChange={(e) => setPushKeyID(e.target.value)}>
                  <option value="">Choose a key…</option>
                  {keys.map((k) => (
                    <option key={k.id} value={k.id}>
                      {k.name}
                    </option>
                  ))}
                </Select>
                <div className="flex gap-2">
                  <Input
                    type="password"
                    aria-label="Current password"
                    placeholder="Current password"
                    value={pushPassword}
                    onChange={(e) => setPushPassword(e.target.value)}
                  />
                  <Button variant="secondary" disabled={!pushKeyID || !pushPassword || pushing} onClick={push}>
                    {pushing ? 'Pushing…' : 'Install key'}
                  </Button>
                </div>
              </div>
            </div>
          )}

        </div>
      </Dialog>
    </>
  )
}

