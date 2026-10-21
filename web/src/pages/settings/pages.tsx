import { useEffect, useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { Copy } from 'lucide-react'
import { api, type APIToken, type AuditEvent, type ServerInfo, type UserAccount } from '@/lib/api'
import { Button, Field, Input, QueryError, Section, Select } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { TZ_LIST, type Settings } from '@/lib/types'
import { fmtTime } from '@/lib/utils'
import { useAuth, WriteGate } from '@/lib/auth'
import { CalendarsSection, GitSyncSection, HASection, SecretStoresSection, SSOSection, UsersSection } from './sections'

// useSettingsDraft loads the server settings once per page and saves the
// whole object back, which is what PUT /settings expects.
function useSettingsDraft() {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [saved, setSaved] = useState('')
  const [error, setError] = useState<unknown>()
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    api
      .getSettings()
      .then((s) => {
        setSettings(s)
        setSaved(JSON.stringify(s))
      })
      .catch(setError)
  }, [])
  const dirty = settings !== null && JSON.stringify(settings) !== saved
  const save = () => {
    if (!settings) return
    setSaving(true)
    api
      .updateSettings(settings)
      .then(() => {
        setSaved(JSON.stringify(settings))
        toast.success('Settings saved')
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setSaving(false))
  }
  return { settings, setSettings, error, dirty, saving, save }
}

function Loading({ error }: { error?: unknown }) {
  if (error) return <QueryError error={error} />
  return <div className="py-12 text-center text-sm text-faint">Loading…</div>
}

function SaveBar({ dirty, saving, onSave }: { dirty: boolean; saving: boolean; onSave: () => void }) {
  const { isAdmin } = useAuth()
  if (!isAdmin) return null
  return (
    <div className="sticky bottom-0 z-10 -mx-1 flex items-center justify-end gap-3 border-t border-line bg-bg/95 px-1 py-3 backdrop-blur">
      <span className="mr-auto text-[13px]" aria-live="polite">
        {dirty ? <span className="font-semibold text-ink">Unsaved changes</span> : <span className="text-faint">All changes saved</span>}
      </span>
      <Button variant="primary" disabled={!dirty || saving} onClick={onSave}>
        {saving ? 'Saving…' : 'Save changes'}
      </Button>
    </div>
  )
}

export function PageIntro({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="mb-5">
      <h2 className="font-display text-[21px] font-semibold tracking-[-0.02em] text-ink">{title}</h2>
      {children && <p className="mt-1 max-w-2xl text-sm text-faint">{children}</p>}
    </div>
  )
}

export function GeneralSettings() {
  const { settings, setSettings, error, dirty, saving, save } = useSettingsDraft()
  const { isAdmin } = useAuth()
  if (!settings) return <Loading error={error} />
  return (
    <div className="space-y-5">
      <PageIntro title="General">Defaults for new jobs and how long history and files are kept.</PageIntro>
      <Section title="Defaults">
        <Field label="Default time zone" hint="New jobs start with this; each job can change it.">
          <Select
            value={settings.default_timezone}
            disabled={!isAdmin}
            onChange={(e) => setSettings({ ...settings, default_timezone: e.target.value })}
            wrapperClassName="max-w-sm"
          >
            {TZ_LIST.map((tz) => (
              <option key={tz} value={tz}>
                {tz}
              </option>
            ))}
          </Select>
        </Field>
      </Section>
      <Section title="Run history">
        <p className="mb-4 text-sm text-faint">Oldest runs are removed first. The defaults keep 100 runs per job for 30 days.</p>
        <div className="grid max-w-lg grid-cols-2 gap-4">
          <Field label="Runs kept per job">
            <Input
              type="number"
              min={10}
              className="font-mono"
              value={settings.retention_runs}
              disabled={!isAdmin}
              onChange={(e) => setSettings({ ...settings, retention_runs: Number(e.target.value) })}
            />
          </Field>
          <Field label="Days kept">
            <Input
              type="number"
              min={1}
              className="font-mono"
              value={settings.retention_days}
              disabled={!isAdmin}
              onChange={(e) => setSettings({ ...settings, retention_days: Number(e.target.value) })}
            />
          </Field>
        </div>
      </Section>
      <Section title="File library">
        <Field
          label="Delete files older than (days)"
          hint="Checked nightly. 0 keeps files forever. Library files are stored unencrypted, so a limit reduces exposure."
        >
          <Input
            type="number"
            min={0}
            className="max-w-40 font-mono"
            value={settings.library_max_age_days ?? 90}
            disabled={!isAdmin}
            onChange={(e) => setSettings({ ...settings, library_max_age_days: Number(e.target.value) })}
          />
        </Field>
      </Section>
      <SaveBar dirty={dirty} saving={saving} onSave={save} />
    </div>
  )
}

export function AccountSettings() {
  const confirm = useConfirm()
  const { username, role } = useAuth()
  const [currentPw, setCurrentPw] = useState('')
  const [newPw, setNewPw] = useState('')
  const [pwSaving, setPwSaving] = useState(false)
  const [tokens, setTokens] = useState<APIToken[]>([])
  const [tokenName, setTokenName] = useState('')
  const [tokenCeiling, setTokenCeiling] = useState('')
  const [tokenExpiry, setTokenExpiry] = useState('90')
  const [newToken, setNewToken] = useState<string | null>(null)

  useEffect(() => {
    api.listTokens().then((r) => setTokens(r.tokens ?? [])).catch(() => undefined)
  }, [])

  const changePassword = () => {
    setPwSaving(true)
    api
      .changePassword(currentPw, newPw)
      .then(() => {
        toast.success('Password updated')
        setCurrentPw('')
        setNewPw('')
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setPwSaving(false))
  }

  const createToken = () => {
    api
      .createToken(tokenName.trim(), tokenCeiling, tokenExpiry ? Number(tokenExpiry) : undefined)
      .then((t) => {
        setNewToken(t.token ?? null)
        setTokenName('')
        return api.listTokens()
      })
      .then((r) => setTokens(r.tokens ?? []))
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
  }

  return (
    <div className="space-y-5">
      <PageIntro title="Account">
        Signed in as <span className="font-semibold text-ink">{username}</span> ({role}).
      </PageIntro>
      <Section title="Password">
        <div className="grid max-w-lg gap-4">
          <Field label="Current password">
            <Input type="password" autoComplete="current-password" value={currentPw} onChange={(e) => setCurrentPw(e.target.value)} />
          </Field>
          <Field label="New password" hint="At least 8 characters." error={newPw && newPw.length < 8 ? 'Too short.' : undefined}>
            <Input type="password" autoComplete="new-password" value={newPw} onChange={(e) => setNewPw(e.target.value)} />
          </Field>
          <div>
            <Button variant="secondary" disabled={pwSaving || !currentPw || newPw.length < 8} onClick={changePassword}>
              {pwSaving ? 'Updating…' : 'Update password'}
            </Button>
          </div>
        </div>
      </Section>
      <Section title="API tokens">
        <p className="mb-4 text-sm text-faint">Long-lived tokens for scripts and CI. A token is shown once, when you create it.</p>
        {newToken && (
          <div role="status" className="mb-4 rounded-xl border border-ok/30 bg-ok-soft p-4">
            <div className="text-sm font-semibold text-ok">Copy this token now. It won’t be shown again.</div>
            <div className="mt-2 flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-md bg-panel px-3 py-2 font-mono text-[13px] text-ink select-all">{newToken}</code>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  void navigator.clipboard.writeText(newToken)
                  toast.success('Token copied')
                }}
              >
                <Copy size={14} /> Copy
              </Button>
            </div>
          </div>
        )}
        <WriteGate>
          <div className="mb-4 flex flex-wrap items-end gap-3">
            <div className="w-56">
              <Field label="Name">
                <Input value={tokenName} onChange={(e) => setTokenName(e.target.value)} placeholder="ci-deploy" />
              </Field>
            </div>
            <div className="w-56">
              <Field label="Access">
                <Select value={tokenCeiling} onChange={(e) => setTokenCeiling(e.target.value)}>
                  <option value="">Same as mine</option>
                  <option value="operator">Operator at most</option>
                  <option value="viewer">Viewer (read-only)</option>
                </Select>
              </Field>
            </div>
            <div className="w-44">
              <Field label="Expires">
                <Select value={tokenExpiry} onChange={(e) => setTokenExpiry(e.target.value)}>
                  <option value="30">In 30 days</option>
                  <option value="90">In 90 days</option>
                  <option value="365">In a year</option>
                  <option value="">Never</option>
                </Select>
              </Field>
            </div>
            <Button variant="primary" disabled={!tokenName.trim()} onClick={createToken}>
              Create token
            </Button>
          </div>
        </WriteGate>
        {tokens.length === 0 ? (
          <p className="text-sm text-faint">No tokens yet.</p>
        ) : (
          <ul className="divide-y divide-line-soft rounded-xl border border-line">
            {tokens.map((t) => (
              <li key={t.id} className="flex items-center gap-3 px-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-semibold text-ink">{t.name}</div>
                  <div className="font-mono text-xs text-faint">
                    {t.prefix}… · {t.role_ceiling ? `${t.role_ceiling} at most` : 'same access as you'} ·{' '}
                    {t.last_used_at ? `last used ${fmtTime(t.last_used_at)}` : 'never used'} ·{' '}
                    {t.expires_at ? (new Date(t.expires_at) < new Date() ? `expired ${fmtTime(t.expires_at)}` : `expires ${fmtTime(t.expires_at)}`) : 'never expires'}
                  </div>
                </div>
                <WriteGate>
                  <Button
                    size="sm"
                    variant="danger-ghost"
                    onClick={() =>
                      confirm({
                        title: `Revoke token ${t.name}?`,
                        body: 'Anything using this token loses access immediately.',
                        confirmLabel: 'Revoke token',
                        destructive: true,
                      }).then(
                        (ok) =>
                          void (ok &&
                            api
                              .deleteToken(t.id)
                              .then(() => setTokens((ts) => ts.filter((x) => x.id !== t.id)))
                              .catch((e) => toast.error(String(e)))),
                      )
                    }
                  >
                    Revoke
                  </Button>
                </WriteGate>
              </li>
            ))}
          </ul>
        )}
      </Section>
    </div>
  )
}

export function UsersSettings() {
  const [users, setUsers] = useState<UserAccount[]>([])
  useEffect(() => {
    api.listUsers().then((r) => setUsers(r.users ?? [])).catch(() => undefined)
  }, [])
  return (
    <div className="space-y-5">
      <PageIntro title="Users">People who can sign in, and what each of them can do.</PageIntro>
      <UsersSection users={users} onChange={setUsers} />
    </div>
  )
}

export function IntegrationsSettings() {
  const { settings, setSettings, error, dirty, saving, save } = useSettingsDraft()
  if (!settings) return <Loading error={error} />
  return (
    <div className="space-y-5">
      <PageIntro title="Integrations">Git sync, single sign-on, secret stores and calendars.</PageIntro>
      <GitSyncSection settings={settings} setSettings={setSettings} />
      <SSOSection settings={settings} setSettings={setSettings} />
      <SaveBar dirty={dirty} saving={saving} onSave={save} />
      <SecretStoresSection />
      <CalendarsSection />
    </div>
  )
}

export function HASettings() {
  const [info, setInfo] = useState<ServerInfo | null>(null)
  useEffect(() => {
    api.getInfo().then(setInfo).catch(() => undefined)
  }, [])
  return (
    <div className="space-y-5">
      <PageIntro title="High availability">Run more than one Kairos server; one leads the scheduler and the others take over if it stops.</PageIntro>
      <HASection info={info} />
    </div>
  )
}

export function AuditSettings() {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [error, setError] = useState<unknown>()
  useEffect(() => {
    api
      .getAudit()
      .then((r) => setEvents(r.events ?? []))
      .catch((e) => setError(e))
  }, [])
  return (
    <div className="space-y-5">
      <PageIntro title="Audit log">Sign-ins, job changes and settings changes, newest first.</PageIntro>
      {error != null ? (
        <QueryError error={error} />
      ) : (
        <div className="rounded-xl border border-line bg-panel">
          {events.length === 0 ? (
            <p className="p-5 text-sm text-faint">No events yet.</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-line-soft text-left text-xs text-faint">
                  <th className="px-5 py-2.5 font-semibold">When</th>
                  <th className="px-3 py-2.5 font-semibold">Who</th>
                  <th className="px-3 py-2.5 font-semibold">What</th>
                  <th className="px-5 py-2.5 font-semibold">Details</th>
                </tr>
              </thead>
              <tbody>
                {events.map((ev) => (
                  <tr key={ev.id} className="border-b border-line-soft last:border-b-0">
                    <td className="px-5 py-2.5 text-xs tabular-nums whitespace-nowrap text-faint">{fmtTime(ev.created_at)}</td>
                    <td className="px-3 py-2.5 text-ink">{ev.actor_username || '—'}</td>
                    <td className="px-3 py-2.5 font-mono text-[13px] text-accent-ink">{ev.action}</td>
                    <td className="px-5 py-2.5 text-[13px] text-faint">
                      {ev.object_type ? `${ev.object_type}${ev.object_id ? ` ${ev.object_id.slice(0, 8)}` : ''}` : ''}
                      {ev.detail ? ` · ${ev.detail}` : ''}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  )
}
