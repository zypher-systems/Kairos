export interface Workflow {
  id: string
  name: string
  description: string
  enabled: boolean
  parameters?: Record<string, string>
  job_count?: number
  created_at: string
  updated_at: string
  schedule_type: ScheduleType
  cron_expr: string
  interval_seconds: number
  oneoff_at?: string | null
  timezone: string
  calendar_id?: string | null
  next_run_at?: string | null
  last_run_at?: string | null
  watch_enabled: boolean
  watch_location_id?: string | null
  watch_path: string
  watch_interval_seconds: number
}

export interface WorkflowEdge {
  id: string
  workflow_id: string
  from_job_id: string
  to_job_id: string
  condition: 'success' | 'failure' | 'always'
}

export interface WorkflowDetail extends Workflow {
  edges: WorkflowEdge[]
  jobs: Job[]
}

export type ScheduleType = 'cron' | 'interval' | 'oneoff' | 'manual'
export type TargetType = 'local' | 'agent' | 'ssh' | 'selector'
export type ExecMode = 'shell' | 'docker' | 'transfer'

export type EndpointKind = 'location' | 'library' | 'ssh' | 'agent'

export interface TransferEndpoint {
  kind: EndpointKind
  location_id?: string | null
  file_id?: string | null
  ssh_host_id?: string | null
  agent_id?: string | null
  path: string
}

export interface TransferConfig {
  source: TransferEndpoint
  dest: TransferEndpoint
  overwrite: boolean
  delete_source: boolean
  preserve_mtime?: boolean
  verify_checksum?: boolean
  from_trigger?: boolean
}

export interface FileEntry {
  id: string
  name: string
  size_bytes: number
  sha256: string
  created_at: string
}

export interface Job {
  id: string
  name: string
  description: string
  schedule_type: ScheduleType
  cron_expr: string
  interval_seconds: number
  oneoff_at?: string | null
  timezone: string
  target_type: TargetType
  target_selector: Record<string, string>
  ssh_host_id?: string | null
  exec_mode: ExecMode
  docker_image: string
  docker_command: string[]
  shell_script: string
  workdir: string
  transfer_config: TransferConfig
  env_plain: Record<string, string>
  env_secrets: Record<string, string>
  env_secret_refs?: EnvSecretRef[]
  retry_count: number
  retry_base_delay_ms: number
  retry_max_delay_ms: number
  timeout_seconds: number
  sla_missed_grace_seconds?: number
  sla_late_seconds?: number
  require_approval?: boolean
  approval_timeout_seconds?: number
  concurrency_policy: string
  enabled: boolean
  tags: string[]
  workflow_id?: string | null
  watch_enabled: boolean
  watch_target: 'local' | 'ssh' | 'agent' | 'location'
  watch_ssh_host_id?: string | null
  watch_agent_id?: string | null
  watch_location_id?: string | null
  watch_path: string
  watch_interval_seconds: number
  watch_last_sig?: string
  notify_channel_ids?: string[]
  calendar_id?: string | null
  next_run_at?: string | null
  last_run_at?: string | null
  created_at: string
  updated_at: string
}

export interface EnvSecretRef {
  name: string
  store_id: string
  path: string
  key: string
}

export interface SecretStore {
  id: string
  name: string
  type: string
  address: string
  mount: string
  namespace?: string
  token?: string
  created_at: string
  updated_at: string
}

export interface Calendar {
  id: string
  name: string
  dates: string[]
  created_at: string
  updated_at: string
}

export type RunStatus = 'queued' | 'claimed' | 'running' | 'succeeded' | 'failed' | 'canceled' | 'skipped' | 'pending_approval'

export interface Run {
  id: string
  job_id: string
  attempt: number
  trigger: string
  status: RunStatus
  target_type: string
  target_id: string
  schedule_time: string
  queued_at: string
  claimed_at?: string | null
  started_at?: string | null
  finished_at?: string | null
  exit_code?: number | null
  error: string
  created_at: string
}

export interface LogChunk {
  seq: number
  stream: 'stdout' | 'stderr' | 'system'
  data: string
}

export interface LogSearchHit {
  run_id: string
  job_id: string
  job_name: string
  seq: number
  stream: string
  snippet: string
  created_at: string
}

export interface AgentInfo {
  id: string
  name: string
  hostname: string
  labels: Record<string, string>
  capabilities: Record<string, string>
  version: string
  online: boolean
  last_heartbeat_at?: string | null
  created_at: string
}

export interface SSHHost {
  id: string
  name: string
  host: string
  port: number
  username: string
  auth_type: string
  ssh_key_id?: string | null
  ssh_key_name?: string
  has_private_key: boolean
  has_password: boolean
  host_key_fingerprint: string
  verify_host_key: boolean
  created_at: string
}

export interface SSHKey {
  id: string
  name: string
  public_key: string
  fingerprint: string
  hosts_using: number
  created_at: string
}

export type ChannelType = 'webhook' | 'discord' | 'slack' | 'smtp'

export interface NotificationChannel {
  id: string
  name: string
  type: ChannelType
  config: Record<string, string>
  enabled: boolean
  notify_on_failure: boolean
  notify_on_success: boolean
  notify_on_missed?: boolean
  notify_on_late?: boolean
  notify_on_pending?: boolean
  notify_on_rejected?: boolean
  created_at: string
  updated_at: string
}

export interface Settings {
  default_timezone: string
  retention_runs: number
  retention_days: number
  library_max_age_days?: number
  git_sync_enabled?: boolean
  git_sync_url?: string
  git_sync_ref?: string
  git_sync_path?: string
  git_sync_interval_seconds?: number
  git_sync_token?: string
  git_sync_has_token?: boolean
  git_sync_last_at?: string | null
  git_sync_last_sha?: string
  git_sync_last_error?: string
  git_sync_last_summary?: string
  sso_enabled?: boolean
  sso_issuer?: string
  sso_client_id?: string
  sso_client_secret?: string
  sso_redirect_url?: string
  sso_has_secret?: boolean
}

export interface Stats {
  queued: number
  running: number
  day_succeeded: number
  day_failed: number
}

export interface ActivityEvent {
  id: string
  job_id?: string
  job_name?: string
  status?: RunStatus
  target_type?: string
  trigger?: string
  exit_code?: number | null
  created_at?: string
}

export interface WSMessage {
  topic: string
  event: string
  data: ActivityEvent | LogChunk | unknown
}

export const TZ_LIST = [
  'UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
  'America/Sao_Paulo', 'Europe/London', 'Europe/Paris', 'Europe/Berlin', 'Europe/Madrid',
  'Europe/Moscow', 'Africa/Cairo', 'Asia/Dubai', 'Asia/Kolkata', 'Asia/Singapore',
  'Asia/Hong_Kong', 'Asia/Shanghai', 'Asia/Tokyo', 'Asia/Seoul', 'Australia/Sydney',
  'Pacific/Auckland',
]
export type StorageKind = 'nfs' | 'smb' | 's3' | 'sftp' | 'mount' | 'agent'

export interface StorageLocation {
  id: string
  name: string
  kind: StorageKind
  base_path: string
  ssh_host_id?: string | null
  agent_id?: string | null
  host?: string
  export?: string
  uid?: number
  gid?: number
  share?: string
  domain?: string
  username?: string
  endpoint?: string
  region?: string
  bucket?: string
  path_style?: boolean
  insecure?: boolean
  access_key?: string
  secret_key?: string
  has_secret_key: boolean
  created_at: string
  updated_at: string
}
