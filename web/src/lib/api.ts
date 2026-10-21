import type { Job, Run, AgentInfo, SSHHost, SSHKey, Settings, Stats, LogChunk, LogSearchHit, FileEntry, NotificationChannel, Workflow, WorkflowDetail, WorkflowEdge, SecretStore, Calendar, StorageLocation } from './types'

export class ApiError extends Error {
  status: number
  /** stable machine-readable code when the server sends one */
  code?: string
  constructor(status: number, message: string, code?: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (res.status === 204) return undefined as T
  if (res.status === 401) {
    if (!path.startsWith('/auth/')) {
      window.location.reload()
      throw new ApiError(401, 'session expired')
    }
  }
  if (!res.ok) {
    let msg = `Request failed (${res.status})`
    let code: string | undefined
    try {
      const body = await res.json()
      if (body?.error) msg = body.error
      if (body?.code) code = body.code
    } catch { /* ignore */ }
    throw new ApiError(res.status, msg, code)
  }
  return res.json() as Promise<T>
}

export const api = {
  // jobs
  listJobs: (params: Record<string, string> = {}) => {
    const qs = new URLSearchParams(params).toString()
    return request<{ jobs: Job[]; total: number }>(`/jobs${qs ? `?${qs}` : ''}`)
  },
  getJob: (id: string) => request<Job>(`/jobs/${id}`),
  createJob: (j: Partial<Job>) => request<Job>('/jobs', { method: 'POST', body: JSON.stringify(j) }),
  updateJob: (id: string, j: Partial<Job>) => request<Job>(`/jobs/${id}`, { method: 'PUT', body: JSON.stringify(j) }),
  deleteJob: (id: string) => request<void>(`/jobs/${id}`, { method: 'DELETE' }),
  runJob: (id: string) => request<Run>(`/jobs/${id}/run`, { method: 'POST' }),

  // runs
  listRuns: (params: Record<string, string> = {}) => {
    const qs = new URLSearchParams(params).toString()
    return request<{ runs: Run[]; total: number }>(`/runs${qs ? `?${qs}` : ''}`)
  },
  getRun: (id: string) => request<Run>(`/runs/${id}`),
  approveRun: (id: string) => request<Run>(`/runs/${id}/approve`, { method: 'POST' }),
  rejectRun: (id: string) => request<Run>(`/runs/${id}/reject`, { method: 'POST' }),
  getLogs: (id: string, after = '0', limit = '200') =>
    request<{ chunks: LogChunk[]; next: number }>(`/runs/${id}/logs?after=${after}&limit=${limit}`),
  searchLogs: (params: Record<string, string>) => {
    const qs = new URLSearchParams(params).toString()
    return request<{ hits: LogSearchHit[]; q: string }>(`/logs/search?${qs}`)
  },

  // targets
  listAgents: () => request<{ agents: AgentInfo[] }>('/agents'),
  createAgent: (name: string, labels: Record<string, string>) =>
    request<{ agent: AgentInfo; token: string }>('/agents', { method: 'POST', body: JSON.stringify({ name, labels }) }),
  deleteAgent: (id: string) => request<void>(`/agents/${id}`, { method: 'DELETE' }),
  updateAgentLabels: (id: string, labels: Record<string, string>) =>
    request<void>(`/agents/${id}/labels`, { method: 'PUT', body: JSON.stringify({ labels }) }),
  listSSHHosts: () => request<{ hosts: SSHHost[] }>('/ssh-hosts'),
  createSSHHost: (h: Record<string, unknown>) =>
    request<SSHHost>('/ssh-hosts', { method: 'POST', body: JSON.stringify(h) }),
  updateSSHHost: (id: string, h: Record<string, unknown>) =>
    request<void>(`/ssh-hosts/${id}`, { method: 'PUT', body: JSON.stringify(h) }),
  deleteSSHHost: (id: string) => request<void>(`/ssh-hosts/${id}`, { method: 'DELETE' }),
  testSSHHost: (id: string) => request<{ ok: boolean; error?: string; fingerprint?: string }>(`/ssh-hosts/${id}/test`, { method: 'POST' }),
  pushInstallAgent: (host_id: string, name: string, labels: string) =>
    request<{ ok: boolean; error?: string; agent?: AgentInfo; output?: string }>(`/ssh-hosts/${host_id}/push-install`, { method: 'POST', body: JSON.stringify({ name, labels }) }),

  agentServerURL: () => request<{ server_url: string; from_config: boolean }>('/agent-server-url'),

  // ssh keys
  listSSHKeys: () => request<{ keys: SSHKey[] }>('/ssh-keys'),
  createSSHKey: (name: string, opts: { generate?: boolean; private_key?: string }) =>
    request<SSHKey>('/ssh-keys', { method: 'POST', body: JSON.stringify({ name, ...opts }) }),
  deleteSSHKey: (id: string) => request<void>(`/ssh-keys/${id}`, { method: 'DELETE' }),
  pushSSHKey: (id: string, host_id: string, password: string) =>
    request<{ ok: boolean; error?: string }>(`/ssh-keys/${id}/push`, { method: 'POST', body: JSON.stringify({ host_id, password }) }),

  // notification channels
  listLocations: () => request<{ locations: StorageLocation[] }>('/storage-locations'),
  createLocation: (l: Partial<StorageLocation>) =>
    request<StorageLocation>('/storage-locations', { method: 'POST', body: JSON.stringify(l) }),
  updateLocation: (id: string, l: Partial<StorageLocation>) =>
    request<StorageLocation>(`/storage-locations/${id}`, { method: 'PUT', body: JSON.stringify(l) }),
  deleteLocation: (id: string) => request<void>(`/storage-locations/${id}`, { method: 'DELETE' }),
  testLocation: (id: string) =>
    request<{ ok: boolean; error?: string; files?: number; sample?: string[] }>(`/storage-locations/${id}/test`, { method: 'POST' }),

  listChannels: () => request<{ channels: NotificationChannel[] }>('/notifications'),
  createChannel: (c: Partial<NotificationChannel>) =>
    request<NotificationChannel>('/notifications', { method: 'POST', body: JSON.stringify(c) }),
  updateChannel: (id: string, c: Partial<NotificationChannel>) =>
    request<NotificationChannel>(`/notifications/${id}`, { method: 'PUT', body: JSON.stringify(c) }),
  deleteChannel: (id: string) => request<void>(`/notifications/${id}`, { method: 'DELETE' }),
  testChannel: (id: string) =>
    request<{ ok: boolean; error?: string }>(`/notifications/${id}/test`, { method: 'POST' }),
  listDeadLetters: () => request<{ dead_letters: DeadLetter[] }>('/notifications/dead-letters'),
  retryDeadLetter: (id: string) => request<void>(`/notifications/dead-letters/${id}/retry`, { method: 'POST' }),
  dismissDeadLetter: (id: string) => request<void>(`/notifications/dead-letters/${id}`, { method: 'DELETE' }),

  listSecretStores: () => request<{ stores: SecretStore[] }>('/secret-stores'),
  createSecretStore: (s: Partial<SecretStore>) =>
    request<SecretStore>('/secret-stores', { method: 'POST', body: JSON.stringify(s) }),
  updateSecretStore: (id: string, s: Partial<SecretStore>) =>
    request<SecretStore>(`/secret-stores/${id}`, { method: 'PUT', body: JSON.stringify(s) }),
  deleteSecretStore: (id: string) => request<void>(`/secret-stores/${id}`, { method: 'DELETE' }),

  listCalendars: () => request<{ calendars: Calendar[] }>('/calendars'),
  createCalendar: (c: Partial<Calendar>) =>
    request<Calendar>('/calendars', { method: 'POST', body: JSON.stringify(c) }),
  updateCalendar: (id: string, c: Partial<Calendar>) =>
    request<Calendar>(`/calendars/${id}`, { method: 'PUT', body: JSON.stringify(c) }),
  deleteCalendar: (id: string) => request<void>(`/calendars/${id}`, { method: 'DELETE' }),

  // workflows
  listWorkflows: () => request<{ workflows: Workflow[] }>('/workflows'),
  getWorkflow: (id: string) => request<WorkflowDetail>(`/workflows/${id}`),
  createWorkflow: (w: { name: string; description?: string; parameters?: Record<string, string> }) =>
    request<Workflow>('/workflows', { method: 'POST', body: JSON.stringify(w) }),
  updateWorkflow: (id: string, w: Partial<Workflow>) =>
    request<Workflow>(`/workflows/${id}`, { method: 'PUT', body: JSON.stringify(w) }),
  deleteWorkflow: (id: string) => request<void>(`/workflows/${id}`, { method: 'DELETE' }),
  replaceWorkflowEdges: (id: string, edges: Partial<WorkflowEdge>[]) =>
    request<{ ok: boolean }>(`/workflows/${id}/edges`, { method: 'PUT', body: JSON.stringify({ edges }) }),
  runWorkflow: (id: string) => request<{ fired: number }>(`/workflows/${id}/run`, { method: 'POST' }),

  // file library
  listFiles: () => request<{ files: FileEntry[] }>('/files'),
  uploadFile: async (file: File, onProgress?: (pct: number) => void) => {
    const xhr = new XMLHttpRequest()
    const done = new Promise<FileEntry>((resolve, reject) => {
      xhr.open('POST', '/api/v1/files')
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && onProgress) onProgress(Math.round((e.loaded / e.total) * 100))
      }
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(JSON.parse(xhr.responseText) as FileEntry)
          } catch {
            reject(new Error('bad server response'))
          }
        } else {
          try {
            reject(new Error(JSON.parse(xhr.responseText)?.error ?? `upload failed (${xhr.status})`))
          } catch {
            reject(new Error(`upload failed (${xhr.status})`))
          }
        }
      }
      xhr.onerror = () => reject(new Error('upload failed'))
    })
    const form = new FormData()
    form.append('file', file)
    xhr.send(form)
    return done
  },
  deleteFile: (id: string) => request<void>(`/files/${id}`, { method: 'DELETE' }),
  fileDownloadURL: (id: string) => `/api/v1/files/${id}/download`,
  fileContent: async (id: string) => {
    const res = await fetch(`/api/v1/files/${id}/content`)
    if (res.status === 401) {
      window.location.reload()
      throw new ApiError(401, 'session expired')
    }
    if (!res.ok) {
      let msg = `Request failed (${res.status})`
      try {
        const body = await res.json()
        if (body?.error) msg = body.error
      } catch {
        /* ignore */
      }
      throw new ApiError(res.status, msg)
    }
    return res.text()
  },

  // settings & stats
  changePassword: (current: string, next: string) =>
    request<void>('/auth/password', { method: 'POST', body: JSON.stringify({ current, new: next }) }),

  getSettings: () => request<Settings>('/settings'),
  updateSettings: (s: Partial<Settings>) => request<Settings>('/settings', { method: 'PUT', body: JSON.stringify(s) }),
  getStats: () => request<Stats>('/stats'),

  exportYAML: async () => {
    const res = await fetch('/api/v1/export')
    if (res.status === 401) {
      window.location.reload()
      throw new ApiError(401, 'session expired')
    }
    if (!res.ok) {
      let msg = `Request failed (${res.status})`
      try {
        const body = await res.json()
        if (body?.error) msg = body.error
      } catch { /* ignore */ }
      throw new ApiError(res.status, msg)
    }
    return res.text()
  },
  applyYAML: (yaml: string, dry_run: boolean) =>
    request<ApplyResult>('/apply', { method: 'POST', body: JSON.stringify({ yaml, dry_run }) }),
  gitSync: (dry_run = false) =>
    request<ApplyResult>('/git-sync', { method: 'POST', body: JSON.stringify({ dry_run }) }),

  getInfo: () => request<ServerInfo>('/info'),

  listTokens: () => request<{ tokens: APIToken[] }>('/tokens'),
  createToken: (name: string, roleCeiling?: string, expiresInDays?: number) =>
    request<APIToken>('/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, role_ceiling: roleCeiling || undefined, expires_in_days: expiresInDays || undefined }),
    }),
  deleteToken: (id: string) => request<void>(`/tokens/${id}`, { method: 'DELETE' }),
  getAudit: () => request<{ events: AuditEvent[] }>('/audit'),

  listUsers: () => request<{ users: UserAccount[] }>('/users'),
  createUser: (u: { username: string; password: string; role: string; team?: string }) =>
    request<UserAccount>('/users', { method: 'POST', body: JSON.stringify(u) }),
  updateUser: (id: string, u: { role: string; team: string; enabled: boolean; password?: string; oidc_subject?: string }) =>
    request<UserAccount>(`/users/${id}`, { method: 'PUT', body: JSON.stringify(u) }),
  deleteUser: (id: string) => request<void>(`/users/${id}`, { method: 'DELETE' }),
}

export interface UserAccount {
  id: string
  username: string
  role: string
  team: string
  enabled: boolean
  oidc_subject?: string
  created_at: string
}

export interface AuditEvent {
  id: string
  actor_id?: string
  actor_username: string
  action: string
  object_type: string
  object_id: string
  detail: string
  ip: string
  created_at: string
}

export interface ApplyChange {
  kind: string
  name: string
  action: string
  changes?: string[]
  error?: string
}

export interface ApplyResult {
  dry_run: boolean
  items: ApplyChange[]
}

export interface ServerInfo {
  name: string
  version: string
  instance_id?: string
  leader?: boolean
  lease_until?: string
}

export interface DeadLetter {
  id: string
  channel_id: string
  payload: { event?: string; job_name?: string; run_id?: string; status?: string }
  attempt: number
  last_error: string
  dead_at?: string | null
  created_at: string
}

export interface APIToken {
  id: string
  name: string
  prefix: string
  token?: string
  role_ceiling?: string | null
  expires_at?: string | null
  last_used_at?: string | null
  created_at: string
}