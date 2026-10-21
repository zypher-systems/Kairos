import { test, expect } from '@playwright/test'

// Every test starts signed in: global-setup.ts saves one session.
async function login(page: import('@playwright/test').Page) {
  await page.goto('/')
  await expect(page.getByRole('link', { name: 'Jobs' })).toBeVisible()
}

async function session(request: import('@playwright/test').APIRequestContext) {
  const body = await (await request.get('/api/v1/auth/status')).json()
  expect(body.authenticated, 'global setup session is missing').toBeTruthy()
}

test('job CRUD, run, and live logs', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'Jobs' }).click()
  await page.getByRole('link', { name: /New job/ }).first().click()
  await page.getByPlaceholder('Daily backup').fill('e2e-echo')
  await page.locator('textarea').first().fill('echo e2e-ok')
  await page.getByRole('button', { name: /Create job/ }).click()
  // The "Job created" toast also contains the name; assert on the list row.
  await expect(page.getByRole('link', { name: /e2e-echo/ })).toBeVisible()
  await page.getByRole('button', { name: 'Run e2e-echo now' }).click()
  await page.getByRole('link', { name: 'Runs' }).click()
  const runRow = page.getByRole('link', { name: /e2e-echo/ }).first()
  await expect(runRow).toBeVisible({ timeout: 15_000 })
  await runRow.click()
  await expect(page.getByText(/e2e-ok/)).toBeVisible({ timeout: 40_000 })
})

test('workflow editor Save is on screen (D28)', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'Workflows' }).click()
  await page.getByRole('button', { name: 'New workflow' }).click()
  await page.getByPlaceholder('nightly-etl').fill('e2e-flow')
  await page.getByRole('button', { name: 'Create' }).click()
  const save = page.getByRole('button', { name: /Save/ })
  await expect(save).toBeVisible()
  await expect(save).toBeInViewport()
  await save.click()
})

test('API: transfer job, agent claim, workflow fan-out', async ({ request }) => {
  await session(request)

  const agentRes = await request.post('/api/v1/agents', {
    data: { name: 'e2e-agent', labels: { role: 'e2e' } },
  })
  expect(agentRes.ok(), await agentRes.text()).toBeTruthy()
  const agentBody = await agentRes.json()
  const token = agentBody.token as string
  const agentID = agentBody.agent.id as string

  const fileRes = await request.post('/api/v1/files', {
    multipart: {
      file: {
        name: 'e2e.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('e2e-file'),
      },
    },
  })
  expect(fileRes.ok(), await fileRes.text()).toBeTruthy()
  const file = await fileRes.json()

  const xfer = await request.post('/api/v1/jobs', {
    data: {
      name: 'e2e-xfer',
      schedule_type: 'interval',
      interval_seconds: 3600,
      timezone: 'UTC',
      target_type: 'agent',
      target_selector: { agent_id: agentID },
      exec_mode: 'transfer',
      enabled: false,
      transfer_config: {
        source: { kind: 'library', file_id: file.id, path: '' },
        dest: { kind: 'agent', agent_id: agentID, path: '/tmp/e2e.txt' },
        overwrite: true,
        delete_source: false,
      },
    },
  })
  expect(xfer.ok(), await xfer.text()).toBeTruthy()

  const jobRes = await request.post('/api/v1/jobs', {
    data: {
      name: 'e2e-agent-job',
      schedule_type: 'interval',
      interval_seconds: 3600,
      timezone: 'UTC',
      target_type: 'agent',
      target_selector: { agent_id: agentID },
      exec_mode: 'shell',
      shell_script: 'echo claimed',
      enabled: false,
    },
  })
  expect(jobRes.ok(), await jobRes.text()).toBeTruthy()
  const job = await jobRes.json()
  const runRes = await request.post(`/api/v1/jobs/${job.id}/run`)
  expect(runRes.ok(), await runRes.text()).toBeTruthy()

  const claim = await request.post('/api/v1/agent/claim', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(claim.ok(), await claim.text()).toBeTruthy()
  const spec = await claim.json()
  expect(spec.run?.run_id).toBeTruthy()

  const a = await request.post('/api/v1/jobs', {
    data: {
      name: 'e2e-wf-a',
      schedule_type: 'interval',
      interval_seconds: 3600,
      timezone: 'UTC',
      target_type: 'local',
      exec_mode: 'shell',
      shell_script: 'true',
      enabled: false,
    },
  })
  const b = await request.post('/api/v1/jobs', {
    data: {
      name: 'e2e-wf-b',
      schedule_type: 'interval',
      interval_seconds: 3600,
      timezone: 'UTC',
      target_type: 'local',
      exec_mode: 'shell',
      shell_script: 'true',
      enabled: false,
    },
  })
  expect(a.ok() && b.ok()).toBeTruthy()
  const ja = await a.json()
  const jb = await b.json()
  const wf = await request.post('/api/v1/workflows', { data: { name: 'e2e-fan', enabled: true } })
  expect(wf.ok(), await wf.text()).toBeTruthy()
  const w = await wf.json()
  const edges = await request.put(`/api/v1/workflows/${w.id}/edges`, {
    data: { edges: [{ from_job_id: ja.id, to_job_id: jb.id, condition: 'always' }] },
  })
  expect(edges.ok(), await edges.text()).toBeTruthy()
  const fired = await request.post(`/api/v1/workflows/${w.id}/run`)
  expect(fired.ok(), await fired.text()).toBeTruthy()
})

test('an admin can create a second user', async ({ request }) => {
  await session(request)
  const res = await request.post('/api/v1/users', {
    data: { username: `viewer-${Date.now()}`, password: 'second-pass-123', role: 'viewer' },
  })
  expect(res.status(), await res.text()).toBe(201)
})

test('SSO start without a provider is a configuration error', async ({ request }) => {
  const res = await request.post('/api/v1/auth/sso/start')
  expect(res.status()).toBe(400)
  expect(await res.text()).toContain('not configured')
})

test('log search is available', async ({ request }) => {
  await session(request)
  const res = await request.get('/api/v1/logs/search?q=hello')
  expect(res.ok(), await res.text()).toBeTruthy()
})

test('job editor leaves the approval gate enabled', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'Jobs' }).click()
  await page.getByRole('link', { name: /New job/ }).first().click()
  await expect(page.getByRole('switch', { name: 'Require approval' })).toBeEnabled()
})

test('a job can require approval', async ({ request }) => {
  await session(request)
  const res = await request.post('/api/v1/jobs', {
    data: {
      name: `need-approve-${Date.now()}`,
      schedule_type: 'interval',
      interval_seconds: 3600,
      timezone: 'UTC',
      target_type: 'local',
      exec_mode: 'shell',
      shell_script: 'true',
      require_approval: true,
    },
  })
  expect(res.status(), await res.text()).toBe(201)
})

test('a job can set a late-run SLA', async ({ request }) => {
  await session(request)
  const res = await request.post('/api/v1/jobs', {
    data: {
      name: `sla-late-${Date.now()}`,
      schedule_type: 'interval',
      interval_seconds: 3600,
      timezone: 'UTC',
      target_type: 'local',
      exec_mode: 'shell',
      shell_script: 'true',
      sla_late_seconds: 30,
    },
  })
  expect(res.status(), await res.text()).toBe(201)
})

test('jobs export as YAML', async ({ request }) => {
  await session(request)
  const res = await request.get('/api/v1/export')
  expect(res.ok(), await res.text()).toBeTruthy()
})

test('job editor offers a secret store for a variable', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'Jobs' }).click()
  await page.getByRole('link', { name: /New job/ }).first().click()
  await page.getByRole('button', { name: 'Add variable' }).click()
  await expect(page.getByRole('option', { name: /From a secret store/ })).toBeEnabled()
})

test('an operator can create a secret store', async ({ request }) => {
  await session(request)
  const res = await request.post('/api/v1/secret-stores', {
    data: {
      name: `prod-vault-${Date.now()}`,
      type: 'vault',
      address: 'http://127.0.0.1:8200',
      mount: 'secret',
      token: 's.x',
    },
  })
  expect(res.status(), await res.text()).toBe(201)
})

test('files page lists newest first and views text', async ({ page }) => {
  await login(page)
  const older = await page.request.post('/api/v1/files', {
    multipart: {
      file: {
        name: 'note.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('older copy'),
      },
    },
  })
  expect(older.ok(), await older.text()).toBeTruthy()
  const newer = await page.request.post('/api/v1/files', {
    multipart: {
      file: {
        name: 'note.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('newest copy'),
      },
    },
  })
  expect(newer.ok(), await newer.text()).toBeTruthy()

  await page.getByRole('link', { name: 'Files' }).click()
  await expect(page.getByText('note.txt').first()).toBeVisible()
  await page.getByRole('button', { name: 'View' }).first().click()
  await expect(page.getByText('newest copy')).toBeVisible()
  await expect(page.getByText('older copy')).toHaveCount(0)
})

test('job editor leaves the skip calendar enabled', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'Jobs' }).click()
  await page.getByRole('link', { name: /New job/ }).first().click()
  await expect(page.getByLabel(/Skip calendar/)).toBeEnabled()
})

test('an operator can create a calendar', async ({ request }) => {
  await session(request)
  const res = await request.post('/api/v1/calendars', {
    data: {
      name: `us-holidays-${Date.now()}`,
      dates: ['2026-12-25'],
    },
  })
  expect(res.status(), await res.text()).toBe(201)
})

test('the license page is gone', async ({ page }) => {
  await login(page)
  await page.goto('/settings/license')
  await expect(page).toHaveURL(/\/settings\/general$/)
  await expect(page.getByRole('link', { name: 'License' })).toHaveCount(0)
})

test('storage locations: create, test and use as transfer ends', async ({ page }) => {
  await login(page)
  // The data directory exists inside the server container, so a mount
  // location on it is testable without any share.
  const created = await page.request.post('/api/v1/storage-locations', {
    data: { name: 'e2e-data', kind: 'mount', base_path: '/var/lib/kairos' },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const loc = await created.json()
  const tested = await page.request.post(`/api/v1/storage-locations/${loc.id}/test`)
  expect(tested.ok()).toBeTruthy()
  expect((await tested.json()).ok, 'mount location must be reachable').toBeTruthy()

  const bad = await page.request.post('/api/v1/storage-locations', {
    data: { name: 'e2e-bad', kind: 's3', bucket: 'b' },
  })
  expect(bad.status(), 'aws s3 without region or endpoint is refused').toBe(400)

  const job = await page.request.post('/api/v1/jobs', {
    data: {
      name: 'e2e-move',
      schedule_type: 'interval',
      interval_seconds: 3600,
      timezone: 'UTC',
      exec_mode: 'transfer',
      target_type: 'ssh',
      transfer_config: {
        source: { kind: 'location', location_id: loc.id, path: 'files/*' },
        dest: { kind: 'location', location_id: loc.id, path: 'e2e-out/' },
        overwrite: true,
        delete_source: false,
      },
      enabled: false,
    },
  })
  expect(job.ok(), await job.text()).toBeTruthy()
  expect((await job.json()).target_type, 'the server derives the target').toBe('local')

  await page.getByRole('link', { name: 'Storage' }).click()
  await expect(page.getByText('e2e-data')).toBeVisible()
  await page.getByRole('button', { name: 'Test' }).first().click()
  await expect(page.getByText(/reachable/)).toBeVisible()
})

test('settings tabs navigate without a redirect loop', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'Settings' }).click()
  await expect(page).toHaveURL(/\/settings\/general$/)
  // Relative links inside a splat route once resolved against the matched
  // path (/settings/general/account), which the catch-all bounced back to
  // /general in an endless replaceState loop that Firefox reports as
  // "The operation is insecure".
  for (const [tab, path] of [
    ['Account', 'account'],
    ['Users', 'users'],
    ['Integrations', 'integrations'],
    ['High availability', 'ha'],
    ['Audit log', 'audit'],
    ['General', 'general'],
  ] as const) {
    await page.getByRole('link', { name: tab, exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/settings/${path}$`))
  }
  await expect(page.getByText('Something went wrong')).toHaveCount(0)
})

test('API: a workflow carries its own schedule and starts every root under one execution', async ({ request }) => {
  await session(request)
  const mk = (name: string) =>
    request.post('/api/v1/jobs', {
      data: { name, schedule_type: 'manual', timezone: 'UTC', target_type: 'local', exec_mode: 'shell', shell_script: 'true', enabled: true },
    })
  const a = await (await mk('e2e-wf-a')).json()
  const b = await (await mk('e2e-wf-b')).json()
  const m = await (await mk('e2e-wf-merge')).json()
  expect(a.next_run_at ?? null, 'manual jobs have no next run').toBeNull()

  const wfRes = await request.post('/api/v1/workflows', {
    data: { name: 'e2e-scheduled', schedule_type: 'cron', cron_expr: '0 3 * * *', timezone: 'UTC' },
  })
  expect(wfRes.ok(), await wfRes.text()).toBeTruthy()
  const wf = await wfRes.json()
  expect(wf.next_run_at, 'a cron schedule yields a next run').toBeTruthy()

  const edges = await request.put(`/api/v1/workflows/${wf.id}/edges`, {
    data: {
      edges: [
        { from_job_id: a.id, to_job_id: m.id, condition: 'success' },
        { from_job_id: b.id, to_job_id: m.id, condition: 'success' },
      ],
    },
  })
  expect(edges.ok(), await edges.text()).toBeTruthy()

  const run = await request.post(`/api/v1/workflows/${wf.id}/run`)
  expect(run.ok(), await run.text()).toBeTruthy()
  expect((await run.json()).fired).toBe(2)
  const runs = await (await request.get(`/api/v1/runs?limit=50`)).json()
  const mine = (runs.runs as { job_id: string; workflow_execution_id?: string }[]).filter((r) => [a.id, b.id, m.id].includes(r.job_id))
  expect(mine.length).toBe(2)
  expect(new Set(mine.map((r) => r.workflow_execution_id)).size, 'one execution for both roots').toBe(1)

  const bad = await request.post('/api/v1/workflows', { data: { name: 'e2e-bad-cron', schedule_type: 'cron', cron_expr: 'nope' } })
  expect(bad.status()).toBe(400)
})
