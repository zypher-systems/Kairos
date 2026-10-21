import { useState, useEffect, type FormEvent } from 'react'
import { LoginScene } from '@/components/LoginScene'
import { Logo } from '@/components/shell/Logo'
import { Button, Field, Input } from '@/components/ui'

export type AuthMode = 'login' | 'setup'

export default function Login({ mode, onAuthed }: { mode: AuthMode; onAuthed: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [ssoAvailable, setSsoAvailable] = useState(false)

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('sso_error')
    if (code) {
      setError(ssoErrorMessage(code))
      window.history.replaceState({}, '', window.location.pathname)
    }
    fetch('/api/v1/auth/status')
      .then((r) => r.json())
      .then((s) => setSsoAvailable(!!s.sso_available))
      .catch(() => undefined)
  }, [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError(null)
    if (password.length < 8) {
      setError('Password must be at least 8 characters')
      return
    }
    if (mode === 'setup' && password !== confirm) {
      setError('Passwords do not match')
      return
    }
    setBusy(true)
    try {
      const res = await fetch(`/api/v1/auth/${mode}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      if (!res.ok) {
        const body = await res.json().catch(() => null)
        throw new Error(body?.error ?? 'Authentication failed')
      }
      onAuthed()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Authentication failed')
    } finally {
      setBusy(false)
    }
  }

  const sso = async () => {
    setError(null)
    setBusy(true)
    try {
      const res = await fetch('/api/v1/auth/sso/start', { method: 'POST' })
      const body = await res.json().catch(() => null)
      if (!res.ok) throw new Error(body?.error ?? 'Single sign-on is unavailable.')
      if (!body?.url) throw new Error('Single sign-on is unavailable.')
      window.location.assign(body.url)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Single sign-on is unavailable.')
      setBusy(false)
    }
  }

  const setup = mode === 'setup'
  return (
    <div className="grid min-h-full bg-bg lg:grid-cols-[minmax(0,1.1fr)_minmax(24rem,1fr)]">
      <section
        className="relative hidden min-h-full flex-col border-r border-line bg-side px-12 py-10 text-ink lg:flex xl:px-16"
      >
        <Logo />
        <div className="flex flex-1 items-center">
          <LoginScene />
        </div>
        <p className="max-w-md font-display text-[24px] leading-tight font-semibold tracking-[-0.02em]">Every job, at the right moment.</p>
        <p className="mt-2 max-w-md text-sm text-faint">
          Schedule scripts and containers across your servers, agents and SSH hosts, and see what ran, when, and why it failed.
        </p>
      </section>

      <section className="flex min-h-full flex-col justify-center px-6 py-12 sm:px-12">
        <div className="mb-10 text-ink lg:hidden">
          <Logo />
        </div>
        <div className="w-full max-w-sm">
          <h1 className="font-display text-[28px] leading-tight font-semibold tracking-[-0.03em] text-ink">
            {setup ? 'Welcome to Kairos' : 'Sign in'}
          </h1>
          <p className="mt-2 text-sm text-faint">
            {setup ? 'Create the first admin account for this installation. You can add more people later.' : 'Sign in to your Kairos account.'}
          </p>

          <form onSubmit={submit} className="mt-8 space-y-5" aria-busy={busy}>
            {error && (
              <p role="alert" className="rounded-lg bg-danger-soft px-4 py-3 text-sm text-danger">
                {error}
              </p>
            )}
            <Field label="Username">
              <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" autoFocus required />
            </Field>
            <Field label="Password" hint={setup ? 'At least 8 characters.' : undefined}>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete={setup ? 'new-password' : 'current-password'}
                required
              />
            </Field>
            {setup && (
              <Field label="Confirm password">
                <Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
              </Field>
            )}
            <Button type="submit" variant="primary" size="lg" className="w-full" disabled={busy || !username}>
              {busy ? 'Working…' : setup ? 'Create account' : 'Continue'}
            </Button>
          </form>
          {mode === 'login' && ssoAvailable && (
            <>
              <div className="my-5 flex items-center gap-3 text-xs text-faint">
                <span className="h-px flex-1 bg-line" /> or <span className="h-px flex-1 bg-line" />
              </div>
              <Button variant="secondary" size="lg" className="w-full" disabled={busy} onClick={sso}>
                Sign in with SSO
              </Button>
            </>
          )}
          <p className="mt-10 text-xs leading-relaxed text-faint">
            {setup
              ? 'Next you’ll connect a host and create your first job.'
              : 'Sessions last seven days. Credentials and secrets are stored encrypted.'}
          </p>
        </div>
      </section>
    </div>
  )
}

function ssoErrorMessage(code: string): string {
  switch (code) {
    case 'unlinked':
      return 'No Kairos account is linked to this sign-in. Ask an admin to add your SSO ID under Settings → Users.'
    case 'disabled':
      return 'That account is disabled.'
    case 'misconfigured':
      return 'Single sign-on is not set up yet.'
    default:
      return 'Single sign-on failed. Try again or use your password.'
  }
}

export function useAuthGating() {
  const [state, setState] = useState<'loading' | 'setup' | 'login' | 'ok'>('loading')
  const [username, setUsername] = useState('')
  const [role, setRole] = useState('')

  const check = async () => {
    try {
      const s = await (await fetch('/api/v1/auth/status')).json()
      if (s.setup_required) setState('setup')
      else if (!s.authenticated) setState('login')
      else {
        setUsername(s.username ?? '')
        setRole(s.role ?? '')
        setState('ok')
      }
    } catch {
      setState('login')
    }
  }

  useEffect(() => {
    check()
  }, [])

  return { state, username, role, refresh: check }
}
