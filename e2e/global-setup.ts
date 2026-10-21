import { request, type FullConfig } from '@playwright/test'

export const USER = 'e2e'
export const PASS = 'e2e-pass-123'
export const STATE = '.auth/state.json'

// Signs in once for the whole run and saves the session cookie. The server
// rate-limits logins (10 per minute per client, successes included), so
// logging in per test trips it.
export default async function globalSetup(config: FullConfig) {
  const { baseURL } = config.projects[0].use
  const ctx = await request.newContext({ baseURL, extraHTTPHeaders: { Origin: baseURL! } })
  const status = await (await ctx.get('/api/v1/auth/status')).json()
  const path = status.setup_required ? '/api/v1/auth/setup' : '/api/v1/auth/login'
  const res = await ctx.post(path, { data: { username: USER, password: PASS } })
  if (!res.ok()) throw new Error(`e2e sign-in failed: ${res.status()} ${await res.text()}`)
  await ctx.storageState({ path: STATE })
  await ctx.dispose()
}
