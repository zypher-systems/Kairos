// Settings sections shared by the settings sub-pages.
import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { api, type UserAccount, type ServerInfo } from '@/lib/api'
import { Button, Input, Field, Select, Textarea, Section, QueryError, Switch, Dialog } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import type { Settings, SecretStore, Calendar } from '@/lib/types'
import { cn, fmtTime } from '@/lib/utils'
import { WriteGate, useAuth } from '@/lib/auth'

export function UsersSection({
  users,
  onChange,
}: {
  users: UserAccount[]
  onChange: (users: UserAccount[]) => void
}) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('operator')
  const [team, setTeam] = useState('')
  const [busy, setBusy] = useState(false)

  const reload = () => api.listUsers().then((r) => onChange(r.users ?? []))

  return (
    <div className="space-y-5">
      <Section title="Add a user">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Field label="Username">
              <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
            </Field>
            <Field label="Password" hint="At least 8 characters.">
              <Input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
            <Field label="Role">
              <Select value={role} onChange={(e) => setRole(e.target.value)}>
                {roleOptions}
              </Select>
            </Field>
            <Field label="Team" hint="Optional label.">
              <Input value={team} onChange={(e) => setTeam(e.target.value)} />
            </Field>
          </div>
          <div className="mt-4">
            <Button
              variant="primary"
              disabled={busy || !username || password.length < 8}
              onClick={() => {
                setBusy(true)
                api
                  .createUser({ username, password, role, team })
                  .then(() => {
                    toast.success(`Added ${username}`)
                    setUsername('')
                    setPassword('')
                    setTeam('')
                    return reload()
                  })
                  .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
                  .finally(() => setBusy(false))
              }}
            >
              Add user
            </Button>
          </div>
        </Section>
      <div className="overflow-x-auto rounded-xl border border-line bg-panel">
        <table className="w-full min-w-[720px] text-sm">
          <thead>
            <tr className="border-b border-line-soft text-left text-xs text-faint">
              <th className="px-5 py-2.5 font-semibold">User</th>
              <th className="px-3 py-2.5 font-semibold">Role</th>
              <th className="px-3 py-2.5 font-semibold">Team</th>
              <th className="px-3 py-2.5 font-semibold">SSO ID</th>
              <th className="px-3 py-2.5 font-semibold">Active</th>
              <th className="px-5 py-2.5">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <UserRow key={u.id} user={u} onDone={reload} />
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-[13px] text-faint">
        Admins manage everything. Operators create and run jobs. Viewers can look but not change anything. The SSO ID links a user to your identity
        provider for single sign-on.
      </p>
    </div>
  )
}

const roleOptions = (
  <>
    <option value="admin">Admin</option>
    <option value="operator">Operator</option>
    <option value="viewer">Viewer</option>
  </>
)

function UserRow({ user, onDone }: { user: UserAccount; onDone: () => void }) {
  const confirm = useConfirm()
  const { username: me } = useAuth()
  const [role, setRole] = useState(user.role)
  const [team, setTeam] = useState(user.team)
  const [enabled, setEnabled] = useState(user.enabled)
  const [oidcSubject, setOidcSubject] = useState(user.oidc_subject ?? '')
  const isMe = user.username === me
  const dirty = role !== user.role || team !== user.team || enabled !== user.enabled || oidcSubject !== (user.oidc_subject ?? '')

  useEffect(() => {
    setRole(user.role)
    setTeam(user.team)
    setEnabled(user.enabled)
    setOidcSubject(user.oidc_subject ?? '')
  }, [user])

  const save = () => {
    api
      .updateUser(user.id, { role, team, enabled, oidc_subject: oidcSubject.trim() })
      .then(() => {
        toast.success(`${user.username} updated`)
        onDone()
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
  }

  return (
    <tr className="border-b border-line-soft last:border-b-0">
      <td className="px-5 py-2.5 font-semibold text-ink">
        {user.username}
        {isMe && <span className="ml-2 text-xs font-normal text-faint">(you)</span>}
      </td>
      <td className="px-3 py-2.5">
        <Select aria-label={`Role for ${user.username}`} wrapperClassName="w-36" value={role} disabled={isMe} onChange={(e) => setRole(e.target.value)}>
          {roleOptions}
        </Select>
      </td>
      <td className="px-3 py-2.5">
        <Input aria-label={`Team for ${user.username}`} className="w-32" value={team} onChange={(e) => setTeam(e.target.value)} />
      </td>
      <td className="px-3 py-2.5">
        <Input aria-label={`SSO ID for ${user.username}`} className="w-44 font-mono" value={oidcSubject} onChange={(e) => setOidcSubject(e.target.value)} />
      </td>
      <td className="px-3 py-2.5">
        <Switch label={`${user.username} can sign in`} checked={enabled} disabled={isMe} onChange={setEnabled} />
      </td>
      <td className="px-5 py-2.5">
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="secondary" disabled={!dirty} onClick={save}>
            Save
          </Button>
          {!isMe && (
            <Button
              size="sm"
              variant="danger-ghost"
              onClick={() =>
                confirm({
                  title: `Delete user ${user.username}?`,
                  body: 'They are signed out and can no longer sign in. Their audit history is kept.',
                  confirmLabel: 'Delete user',
                  destructive: true,
                }).then(
                  (ok) =>
                    void (ok &&
                      api
                        .deleteUser(user.id)
                        .then(() => {
                          toast.success(`${user.username} deleted`)
                          onDone()
                        })
                        .catch((e) => toast.error(String(e)))),
                )
              }
            >
              Delete
            </Button>
          )}
        </div>
      </td>
    </tr>
  )
}

export function GitSyncSection({
  settings,
  setSettings,
}: {
  settings: Settings
  setSettings: (s: Settings) => void
}) {
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const pull = (dry: boolean) => {
    setBusy(true)
    api
      .gitSync(dry)
      .then((r) => {
        const n = (r.items ?? []).length
        toast.success(dry ? `Dry run: ${n} documents` : `Applied ${n} documents`)
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setBusy(false))
  }
  return (
    <Section index="04c" title="Git sync" className="lg:col-span-2">
      <p className="mb-4 text-sm text-faint">
        Kairos pulls a git repository on a schedule and applies the job and workflow YAML in it. Use an HTTPS URL, with an access token for private
        repositories.
      </p>
      <div className="space-y-4">
          <Field label="Enabled">
            <Switch
              checked={!!settings.git_sync_enabled}
              onChange={(v) => setSettings({ ...settings, git_sync_enabled: v })}
            />
          </Field>
          <Field label="Repository URL" hint="https:// only.">
            <Input
              value={settings.git_sync_url ?? ''}
              onChange={(e) => setSettings({ ...settings, git_sync_url: e.target.value })}
              placeholder="https://github.com/org/jobs.git"
              className="font-mono"
            />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Ref">
              <Input
                value={settings.git_sync_ref ?? 'main'}
                onChange={(e) => setSettings({ ...settings, git_sync_ref: e.target.value })}
                className="font-mono"
              />
            </Field>
            <Field label="Path" hint="Directory or file inside the repo.">
              <Input
                value={settings.git_sync_path ?? '.'}
                onChange={(e) => setSettings({ ...settings, git_sync_path: e.target.value })}
                className="font-mono"
              />
            </Field>
          </div>
          <Field label="Interval (seconds)" hint="Minimum 60.">
            <Input
              type="number"
              min={60}
              className="font-mono"
              value={settings.git_sync_interval_seconds ?? 300}
              onChange={(e) => setSettings({ ...settings, git_sync_interval_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="Token" hint={settings.git_sync_has_token ? 'A token is stored. Leave blank to keep it.' : 'Optional PAT for private repos.'}>
            <Input
              type="password"
              value={token}
              onChange={(e) => {
                setToken(e.target.value)
                setSettings({ ...settings, git_sync_token: e.target.value })
              }}
              placeholder={settings.git_sync_has_token ? '••••••' : ''}
            />
          </Field>
          {(settings.git_sync_last_at || settings.git_sync_last_error) && (
            <p className="font-mono text-[11px] text-faint">
              last {settings.git_sync_last_at ? fmtTime(settings.git_sync_last_at) : '—'}
              {settings.git_sync_last_sha ? ` · ${settings.git_sync_last_sha.slice(0, 8)}` : ''}
              {settings.git_sync_last_summary ? ` · ${settings.git_sync_last_summary}` : ''}
              {settings.git_sync_last_error ? ` · ${settings.git_sync_last_error}` : ''}
            </p>
          )}
          <div className="flex gap-3">
            <Button variant="outline" disabled={busy || !settings.git_sync_url} onClick={() => pull(true)}>
              Dry run now
            </Button>
            <Button variant="outline" disabled={busy || !settings.git_sync_url} onClick={() => pull(false)}>
              Sync now
            </Button>
          </div>
      </div>
    </Section>
  )
}

export function SSOSection({
  settings,
  setSettings,
}: {
  settings: Settings
  setSettings: (s: Settings) => void
}) {
  return (
    <Section index="04d" title="Single sign-on" className="lg:col-span-2">
      <p className="mb-4 text-sm text-faint">
        People sign in with your identity provider (OIDC). Each Kairos user is linked to their provider ID under Users; Kairos does not create accounts
        or map groups to roles. Password sign-in keeps working.
      </p>
      <div className="space-y-4">
          <Field label="Enabled">
            <Switch
              checked={!!settings.sso_enabled}
              onChange={(v) => setSettings({ ...settings, sso_enabled: v })}
            />
          </Field>
          <Field label="Issuer URL" hint="OpenID Provider issuer, used for discovery.">
            <Input
              value={settings.sso_issuer ?? ''}
              onChange={(e) => setSettings({ ...settings, sso_issuer: e.target.value })}
              placeholder="https://idp.example/realms/kairos"
              className="font-mono"
            />
          </Field>
          <Field label="Client ID">
            <Input
              value={settings.sso_client_id ?? ''}
              onChange={(e) => setSettings({ ...settings, sso_client_id: e.target.value })}
              className="font-mono"
            />
          </Field>
          <Field
            label="Client secret"
            hint={settings.sso_has_secret ? 'A secret is stored. Leave blank to keep it.' : 'Confidential client secret.'}
          >
            <Input
              type="password"
              value={settings.sso_client_secret ?? ''}
              onChange={(e) => setSettings({ ...settings, sso_client_secret: e.target.value })}
              placeholder={settings.sso_has_secret ? '••••••' : ''}
            />
          </Field>
          <Field
            label="Redirect URL"
            hint="Must match the IdP client. Example: https://kairos.example/api/v1/auth/sso/callback"
          >
            <Input
              value={settings.sso_redirect_url ?? ''}
              onChange={(e) => setSettings({ ...settings, sso_redirect_url: e.target.value })}
              placeholder="https://kairos.example/api/v1/auth/sso/callback"
              className="font-mono"
            />
          </Field>
      </div>
    </Section>
  )
}

export function SecretStoresSection() {
  const confirm = useConfirm()
  const [stores, setStores] = useState<SecretStore[]>([])
  const [error, setError] = useState<unknown>()
  const [editing, setEditing] = useState<SecretStore | null>(null)

  const reload = useCallback(() => {
    setError(undefined)
    api
      .listSecretStores()
      .then((r) => setStores(r.stores ?? []))
      .catch((e) => setError(e))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const remove = (st: SecretStore) => {
    confirm({ title: `Delete secret store "${st.name}"?`, body: 'Jobs that read secrets from this store will no longer get them.', confirmLabel: 'Delete store', destructive: true }).then((ok) => void (ok &&
api
      .deleteSecretStore(st.id)
      .then(() => {
        toast.success(`Store "${st.name}" deleted`)
        reload()
      })
      .catch((e) => toast.error(String(e)))))
  }

  return (
    <Section
      index="04e"
      title="Secret stores"
      className="lg:col-span-2"
      right={
        <WriteGate>
          <StoreDialog onDone={reload} />
        </WriteGate>
      }
    >
      <p className="mb-4 text-sm text-faint">
        HashiCorp Vault (KV v2). Kairos reads a secret when a run starts and never stores its value. Secrets typed into a job work without this.
      </p>
      {error != null ? <QueryError error={error} /> : null}
      {stores.map((st) => (
          <div key={st.id} className="flex flex-wrap items-center justify-between gap-2 border-b border-line-soft py-2">
            <div>
              <div className="text-[13px] font-medium text-ink">{st.name}</div>
              <div className="font-mono text-[12px] text-faint">
                {st.address} · {st.mount}
                {st.namespace ? ` · ${st.namespace}` : ''}
              </div>
            </div>
            <WriteGate>
              <div className="flex gap-3">
                <Button size="sm" variant="ghost" onClick={() => setEditing(st)}>
                  Edit
                </Button>
                <Button size="sm" variant="ghost" onClick={() => remove(st)}>
                  Delete
                </Button>
              </div>
            </WriteGate>
          </div>
        ))}
      {stores.length === 0 && error == null && (
        <p className="text-[13px] text-dim">No stores yet. Add a Vault address, KV mount, and token.</p>
      )}
      {editing && (
        <StoreDialog
          store={editing}
          onDone={() => {
            setEditing(null)
            reload()
          }}
          onClose={() => setEditing(null)}
        />
      )}
    </Section>
  )
}

function StoreDialog({
  store,
  onDone,
  onClose,
}: {
  store?: SecretStore
  onDone: () => void
  onClose?: () => void
}) {
  const isEdit = !!store
  const [open, setOpen] = useState(isEdit)
  const [busy, setBusy] = useState(false)
  const [name, setName] = useState(store?.name ?? '')
  const [address, setAddress] = useState(store?.address ?? '')
  const [mount, setMount] = useState(store?.mount ?? 'secret')
  const [namespace, setNamespace] = useState(store?.namespace ?? '')
  const [token, setToken] = useState('')

  const close = () => {
    setOpen(false)
    onClose?.()
  }

  const valid = name.trim() !== '' && address.trim() !== '' && (isEdit || token.trim() !== '')

  const save = () => {
    setBusy(true)
    const payload: Partial<SecretStore> = {
      name: name.trim(),
      type: 'vault',
      address: address.trim(),
      mount: mount.trim() || 'secret',
      namespace: namespace.trim(),
      token: token.trim(),
    }
    const req = isEdit ? api.updateSecretStore(store!.id, payload) : api.createSecretStore(payload)
    req
      .then(() => {
        toast.success(isEdit ? 'Store updated' : `Store "${name}" created`)
        onDone()
        close()
      })
      .catch((e) => toast.error(String(e)))
      .finally(() => setBusy(false))
  }

  return (
    <>
      {!isEdit && (
        <Button variant="primary" onClick={() => setOpen(true)}>
          New store →
        </Button>
      )}
      <Dialog
        open={open}
        onClose={close}
        title={isEdit ? `edit store · ${store!.name}` : 'new Vault store'}
        footer={
          <>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button variant="primary" onClick={save} disabled={!valid || busy}>
              {busy ? 'Saving…' : isEdit ? 'Save changes' : 'Create store'}
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="prod-vault" />
          </Field>
          <Field label="Address" hint="Vault API base, including scheme.">
            <Input
              className="font-mono"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              placeholder="https://vault.example:8200"
            />
          </Field>
          <Field label="KV mount">
            <Input className="font-mono" value={mount} onChange={(e) => setMount(e.target.value)} placeholder="secret" />
          </Field>
          <Field label="Namespace" hint="Optional. Enterprise / HCP namespaces.">
            <Input className="font-mono" value={namespace} onChange={(e) => setNamespace(e.target.value)} />
          </Field>
          <Field label="Token" hint={isEdit ? 'Leave blank to keep the stored token.' : 'Sealed at rest. Never shown again.'}>
            <Input
              type="password"
              className="font-mono [color-scheme:dark]"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder={isEdit ? '••••••' : ''}
            />
          </Field>
        </div>
      </Dialog>
    </>
  )
}

export function CalendarsSection() {
  const confirm = useConfirm()
  const [calendars, setCalendars] = useState<Calendar[]>([])
  const [error, setError] = useState<unknown>()
  const [editing, setEditing] = useState<Calendar | null>(null)

  const reload = useCallback(() => {
    setError(undefined)
    api
      .listCalendars()
      .then((r) => setCalendars(r.calendars ?? []))
      .catch((e) => setError(e))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const remove = (cal: Calendar) => {
    confirm({ title: `Delete calendar "${cal.name}"?`, body: 'Jobs that skip days on this calendar will run on every scheduled day.', confirmLabel: 'Delete calendar', destructive: true }).then((ok) => void (ok &&
api
      .deleteCalendar(cal.id)
      .then(() => {
        toast.success(`Calendar "${cal.name}" deleted`)
        reload()
      })
      .catch((e) => toast.error(String(e)))))
  }

  return (
    <Section
      index="04f"
      title="Calendars"
      className="lg:col-span-2"
      right={
        <WriteGate>
          <CalendarDialog onDone={reload} />
        </WriteGate>
      }
    >
      <p className="mb-4 text-sm text-faint">
        Lists of days to skip, such as public holidays. Scheduled runs on those dates (in the job’s time zone) are skipped; one-off and manual runs are
        not.
      </p>
      {error != null ? <QueryError error={error} /> : null}
      {calendars.map((cal) => (
          <div key={cal.id} className="flex flex-wrap items-center justify-between gap-2 border-b border-line-soft py-2">
            <div>
              <div className="text-[13px] font-medium text-ink">{cal.name}</div>
              <div className="font-mono text-[12px] text-faint">
                {cal.dates.length === 0 ? 'no dates' : `${cal.dates.length} date${cal.dates.length === 1 ? '' : 's'}`}
              </div>
            </div>
            <WriteGate>
              <div className="flex gap-3">
                <Button size="sm" variant="ghost" onClick={() => setEditing(cal)}>
                  Edit
                </Button>
                <Button size="sm" variant="ghost" onClick={() => remove(cal)}>
                  Delete
                </Button>
              </div>
            </WriteGate>
          </div>
        ))}
      {calendars.length === 0 && error == null && (
        <p className="text-[13px] text-dim">No calendars yet. Add a name and one date per line.</p>
      )}
      {editing && (
        <CalendarDialog
          calendar={editing}
          onDone={() => {
            setEditing(null)
            reload()
          }}
          onClose={() => setEditing(null)}
        />
      )}
    </Section>
  )
}

function CalendarDialog({
  calendar,
  onDone,
  onClose,
}: {
  calendar?: Calendar
  onDone: () => void
  onClose?: () => void
}) {
  const isEdit = !!calendar
  const [open, setOpen] = useState(isEdit)
  const [busy, setBusy] = useState(false)
  const [name, setName] = useState(calendar?.name ?? '')
  const [datesText, setDatesText] = useState((calendar?.dates ?? []).join('\n'))

  const close = () => {
    setOpen(false)
    onClose?.()
  }

  const dates = datesText
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
  const valid = name.trim() !== ''

  const save = () => {
    setBusy(true)
    const payload: Partial<Calendar> = { name: name.trim(), dates }
    const req = isEdit ? api.updateCalendar(calendar!.id, payload) : api.createCalendar(payload)
    req
      .then(() => {
        toast.success(isEdit ? 'Calendar updated' : `Calendar "${name}" created`)
        onDone()
        close()
      })
      .catch((e) => toast.error(String(e)))
      .finally(() => setBusy(false))
  }

  return (
    <>
      {!isEdit && (
        <Button variant="primary" onClick={() => setOpen(true)}>
          New calendar →
        </Button>
      )}
      <Dialog
        open={open}
        onClose={close}
        title={isEdit ? `edit calendar · ${calendar!.name}` : 'new skip calendar'}
        footer={
          <>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button variant="primary" onClick={save} disabled={!valid || busy}>
              {busy ? 'Saving…' : isEdit ? 'Save changes' : 'Create calendar'}
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="us-holidays" />
          </Field>
          <Field label="Skip dates" hint="One YYYY-MM-DD per line. Job timezone, not UTC.">
            <Textarea
              className="font-mono min-h-32"
              value={datesText}
              onChange={(e) => setDatesText(e.target.value)}
              placeholder={'2026-12-25\n2027-01-01'}
            />
          </Field>
        </div>
      </Dialog>
    </>
  )
}

export function HASection({ info }: { info: ServerInfo | null }) {
  return (
    <Section index="04g" title="High availability" className="lg:col-span-2">
      <p className="mb-4 text-sm text-faint">
        Extra servers share the database and <span className="font-mono">KAIROS_SECRET</span>; only the leader runs the scheduler. Followers keep serving
        the API and the UI, and take over if the leader stops.
      </p>
      {info && (
        <div className="mb-3 flex flex-wrap items-center gap-3 border-y border-line py-3">
          <span className={cn('h-2 w-2 rounded-full', info.leader ? 'bg-ok' : 'bg-warn')} />
          <span className="text-[14px] font-semibold text-ink">
            {info.leader ? 'scheduler leader' : 'follower'}
          </span>
          {info.instance_id ? (
            <span className="font-mono text-[11px] text-faint">{info.instance_id}</span>
          ) : null}
        </div>
      )}
    </Section>
  )
}
