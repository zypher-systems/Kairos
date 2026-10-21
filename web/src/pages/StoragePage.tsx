import { useState } from 'react'
import { toast } from 'sonner'
import { Cloud, FolderTree, HardDrive, Network, Plus, Server } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Button, Dialog, EmptyState, Field, Input, QueryError, Select, Switch, RowMenu, MenuAction } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { useAgents, useSSHHosts, useStorageLocations } from '@/hooks/queries'
import { cn } from '@/lib/utils'
import type { StorageKind, StorageLocation } from '@/lib/types'
import { WriteGate } from '@/lib/auth'

const kindMeta: Record<StorageKind, { icon: typeof HardDrive; label: string; blurb: string }> = {
  nfs: { icon: Network, label: 'NFS share', blurb: 'An NFS export on a NAS or file server. Kairos talks to it directly; nothing to mount.' },
  smb: { icon: Network, label: 'SMB / Windows share', blurb: 'A Windows, Samba or NAS share with a user name and password. Nothing to mount.' },
  s3: { icon: Cloud, label: 'S3 bucket', blurb: 'A prefix in an S3-compatible bucket: AWS, MinIO, Ceph, Wasabi and others.' },
  sftp: { icon: Server, label: 'SFTP host', blurb: 'A directory on an SSH host, using the host’s stored credentials.' },
  mount: { icon: FolderTree, label: 'Mounted directory', blurb: 'A directory already mounted into the Kairos server container, or its own disk.' },
  agent: { icon: HardDrive, label: 'Agent directory', blurb: 'A directory on an agent machine. Transfers touching it run on that agent.' },
}

function summary(l: StorageLocation, hosts: { id: string; name: string }[], agents: { id: string; name: string }[]): string {
  switch (l.kind) {
    case 'nfs':
      return `${l.host}:${l.export}${l.base_path ? '/' + l.base_path : ''}`
    case 'smb':
      return `//${l.host}/${l.share}${l.base_path ? '/' + l.base_path : ''}`
    case 'mount':
      return l.base_path
    case 'sftp':
      return `${hosts.find((h) => h.id === l.ssh_host_id)?.name ?? 'host?'}:${l.base_path || '/'}`
    case 's3':
      return `${l.endpoint || `s3 ${l.region ?? ''}`.trim()}/${l.bucket}${l.base_path ? '/' + l.base_path : ''}`
    case 'agent':
      return `${agents.find((a) => a.id === l.agent_id)?.name ?? 'agent?'}:${l.base_path}`
  }
}

export default function StoragePage() {
  const qc = useQueryClient()
  const confirm = useConfirm()
  const { data: locations, error } = useStorageLocations()
  const { data: hosts = [] } = useSSHHosts()
  const { data: agents = [] } = useAgents()
  const [editing, setEditing] = useState<StorageLocation | null>(null)
  const [adding, setAdding] = useState(false)
  const [testing, setTesting] = useState<string | null>(null)

  const reload = () => qc.invalidateQueries({ queryKey: ['storage-locations'] })

  const test = (l: StorageLocation) => {
    setTesting(l.id)
    api
      .testLocation(l.id)
      .then((r) => {
        if (r.ok) toast.success(`${l.name}: reachable, ${r.files ?? 0} file${r.files === 1 ? '' : 's'} at the base path`)
        else toast.error(`${l.name}: ${r.error ?? 'unreachable'}`)
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setTesting(null))
  }

  const remove = (l: StorageLocation) =>
    confirm({
      title: `Remove ${l.name}?`,
      body: 'Jobs that use this location keep their settings and fail at run time until they are pointed elsewhere.',
      confirmLabel: 'Remove location',
      destructive: true,
    }).then(
      (ok) =>
        void (ok &&
          api
            .deleteLocation(l.id)
            .then(() => {
              toast.success(`${l.name} removed`)
              reload()
            })
            .catch((e) => toast.error(String(e)))),
    )

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Storage</h1>
          <p className="mt-1.5 max-w-2xl text-sm text-faint">
            The places files live: mounted shares, SFTP hosts, S3 buckets and agent directories. Transfers move files between them, and file
            triggers watch them. Nothing has to pass through the Kairos server.
          </p>
        </div>
        <WriteGate>
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus size={16} strokeWidth={2.4} /> Add a location
          </Button>
        </WriteGate>
      </header>

      {error != null ? <QueryError error={error} /> : null}

      <div className="rounded-2xl border border-line bg-panel">
        {locations && locations.length === 0 && (
          <EmptyState
            icon={HardDrive}
            title="No storage locations yet"
            sub="Point Kairos at an NFS share, an SFTP drop folder or a bucket, then move files between them on a schedule or when they arrive."
            action={
              <WriteGate>
                <Button variant="primary" onClick={() => setAdding(true)}>
                  Add a location
                </Button>
              </WriteGate>
            }
          />
        )}
        {locations && locations.length > 0 && (
          <ul className="divide-y divide-line-soft">
            {locations.map((l) => {
              const meta = kindMeta[l.kind]
              const Icon = meta.icon
              return (
                <li key={l.id} className="flex flex-wrap items-center gap-4 px-5 py-4">
                  <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-raised text-dim">
                    <Icon size={18} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-semibold text-ink">{l.name}</div>
                    <div className="truncate font-mono text-xs text-faint">
                      {meta.label} · {summary(l, hosts, agents)}
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <Button size="sm" variant="secondary" disabled={testing === l.id || l.kind === 'agent'} onClick={() => test(l)}>
                      {testing === l.id ? 'Testing…' : 'Test'}
                    </Button>
                    <WriteGate>
                      <RowMenu label={`Actions for ${l.name}`}>
                        <MenuAction onSelect={() => setEditing(l)}>Edit</MenuAction>
                        <MenuAction danger onSelect={() => remove(l)}>
                          Remove…
                        </MenuAction>
                      </RowMenu>
                    </WriteGate>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      {(adding || editing) && (
        <LocationDialog
          location={editing ?? undefined}
          hosts={hosts}
          agents={agents}
          onDone={() => {
            setAdding(false)
            setEditing(null)
            reload()
          }}
          onClose={() => {
            setAdding(false)
            setEditing(null)
          }}
        />
      )}
    </div>
  )
}

function LocationDialog({
  location,
  hosts,
  agents,
  onDone,
  onClose,
}: {
  location?: StorageLocation
  hosts: { id: string; name: string; host: string }[]
  agents: { id: string; name: string; online: boolean }[]
  onDone: () => void
  onClose: () => void
}) {
  const isEdit = !!location
  const [kind, setKind] = useState<StorageKind>(location?.kind ?? 'nfs')
  const [name, setName] = useState(location?.name ?? '')
  const [basePath, setBasePath] = useState(location?.base_path ?? '')
  const [hostId, setHostId] = useState(location?.ssh_host_id ?? '')
  const [agentId, setAgentId] = useState(location?.agent_id ?? '')
  const [endpoint, setEndpoint] = useState(location?.endpoint ?? '')
  const [region, setRegion] = useState(location?.region ?? '')
  const [bucket, setBucket] = useState(location?.bucket ?? '')
  const [pathStyle, setPathStyle] = useState(location?.path_style ?? false)
  const [insecure, setInsecure] = useState(location?.insecure ?? false)
  const [accessKey, setAccessKey] = useState(location?.access_key ?? '')
  const [secretKey, setSecretKey] = useState('')
  const [host, setHost] = useState(location?.host ?? '')
  const [exportPath, setExportPath] = useState(location?.export ?? '')
  const [uid, setUid] = useState(String(location?.uid ?? 0))
  const [gid, setGid] = useState(String(location?.gid ?? 0))
  const [share, setShare] = useState(location?.share ?? '')
  const [domain, setDomain] = useState(location?.domain ?? '')
  const [username, setUsername] = useState(location?.username ?? '')
  const [saving, setSaving] = useState(false)

  const valid =
    name.trim() !== '' &&
    (kind === 'mount' ? basePath.startsWith('/') : true) &&
    (kind === 'nfs' ? host.trim() !== '' && exportPath.trim().startsWith('/') : true) &&
    (kind === 'smb' ? host.trim() !== '' && share.trim() !== '' && username.trim() !== '' && (isEdit || secretKey !== '') : true) &&
    (kind === 'sftp' ? hostId !== '' : true) &&
    (kind === 'agent' ? agentId !== '' && basePath.startsWith('/') : true) &&
    (kind === 's3' ? bucket.trim() !== '' && (endpoint.trim() !== '' || region.trim() !== '') : true)

  const save = () => {
    setSaving(true)
    const body: Partial<StorageLocation> = {
      name: name.trim(),
      kind,
      base_path: basePath.trim(),
      ssh_host_id: kind === 'sftp' ? hostId : null,
      agent_id: kind === 'agent' ? agentId : null,
      endpoint: kind === 's3' ? endpoint.trim() : '',
      region: kind === 's3' ? region.trim() : '',
      bucket: kind === 's3' ? bucket.trim() : '',
      path_style: kind === 's3' ? pathStyle : false,
      insecure: kind === 's3' ? insecure : false,
      access_key: kind === 's3' ? accessKey.trim() : '',
      secret_key: (kind === 's3' || kind === 'smb') && secretKey ? secretKey : undefined,
      host: kind === 'nfs' || kind === 'smb' ? host.trim() : '',
      export: kind === 'nfs' ? exportPath.trim() : '',
      uid: kind === 'nfs' ? Number(uid) || 0 : 0,
      gid: kind === 'nfs' ? Number(gid) || 0 : 0,
      share: kind === 'smb' ? share.trim() : '',
      domain: kind === 'smb' ? domain.trim() : '',
      username: kind === 'smb' ? username.trim() : '',
    }
    const req = isEdit ? api.updateLocation(location.id, body) : api.createLocation(body)
    req
      .then(() => {
        toast.success(isEdit ? `${body.name} updated` : `${body.name} added`)
        onDone()
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
      .finally(() => setSaving(false))
  }

  return (
    <Dialog
      open
      onClose={onClose}
      title={isEdit ? `Edit ${location.name}` : 'Add a storage location'}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" disabled={!valid || saving} onClick={save}>
            {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Add location'}
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        {!isEdit && (
          <div className="grid gap-2 sm:grid-cols-2">
            {(Object.keys(kindMeta) as StorageKind[]).map((k) => {
              const m = kindMeta[k]
              const Icon = m.icon
              return (
                <button
                  key={k}
                  type="button"
                  aria-pressed={kind === k}
                  onClick={() => setKind(k)}
                  className={cn(
                    'flex items-start gap-3 rounded-xl border p-3 text-left transition-colors cursor-pointer',
                    kind === k ? 'border-accent bg-accent-soft' : 'border-line-soft hover:bg-raised',
                  )}
                >
                  <Icon size={18} className="mt-0.5 shrink-0 text-dim" />
                  <span>
                    <span className="block text-sm font-semibold text-ink">{m.label}</span>
                    <span className="block text-[12px] text-faint">{m.blurb}</span>
                  </span>
                </button>
              )
            })}
          </div>
        )}
        <Field label="Name" hint="How jobs refer to it.">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={kind === 's3' ? 'reports-bucket' : kind === 'smb' ? 'finance-share' : 'nas-incoming'} />
        </Field>
        {kind === 'nfs' && (
          <div className="grid gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="NFS server" hint="Host name or IP of the NAS or file server.">
                <Input className="font-mono" value={host} onChange={(e) => setHost(e.target.value)} placeholder="nas.internal" />
              </Field>
              <Field label="Export" hint="The exported path, as listed by the server (showmount -e).">
                <Input className="font-mono" value={exportPath} onChange={(e) => setExportPath(e.target.value)} placeholder="/volume1/incoming" />
              </Field>
              <Field label="Sub-directory" hint="Optional. Paths in jobs are relative to it.">
                <Input className="font-mono" value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="drop" />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label="UID" hint="Identity the export sees.">
                  <Input type="number" min={0} className="font-mono" value={uid} onChange={(e) => setUid(e.target.value)} />
                </Field>
                <Field label="GID">
                  <Input type="number" min={0} className="font-mono" value={gid} onChange={(e) => setGid(e.target.value)} />
                </Field>
              </div>
            </div>
            <p className="text-[12px] text-faint">
              NFSv3. Kairos connects from a non-privileged port, so the export must allow that (the <span className="font-mono">insecure</span> option
              on Linux; “allow non-privileged ports” on a NAS). Files are read and written as the UID/GID above, or as the export maps them.
            </p>
          </div>
        )}
        {kind === 'smb' && (
          <div className="grid gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Server" hint="Host name or IP; port 445.">
                <Input className="font-mono" value={host} onChange={(e) => setHost(e.target.value)} placeholder="files.corp.internal" />
              </Field>
              <Field label="Share" hint="The share name only.">
                <Input className="font-mono" value={share} onChange={(e) => setShare(e.target.value)} placeholder="exports" />
              </Field>
              <Field label="Sub-directory" hint="Optional. Paths in jobs are relative to it.">
                <Input className="font-mono" value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="incoming" />
              </Field>
              <Field label="Domain" hint="Optional (Active Directory).">
                <Input className="font-mono" value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="CORP" />
              </Field>
              <Field label="User name">
                <Input className="font-mono" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
              </Field>
              <Field label="Password" hint={isEdit && location.has_secret_key ? 'Leave blank to keep the stored password.' : 'Sealed at rest. Never shown again.'}>
                <Input type="password" className="font-mono" value={secretKey} onChange={(e) => setSecretKey(e.target.value)} autoComplete="new-password" />
              </Field>
            </div>
          </div>
        )}
        {kind === 'mount' && (
          <Field label="Directory on the Kairos server" hint="Mount the share into the container and give its path here, e.g. /mnt/incoming.">
            <Input className="font-mono" value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="/mnt/incoming" />
          </Field>
        )}
        {kind === 'sftp' && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="SSH host">
              <Select value={hostId} onChange={(e) => setHostId(e.target.value)}>
                <option value="">Choose a host…</option>
                {hosts.map((h) => (
                  <option key={h.id} value={h.id}>
                    {h.name} ({h.host})
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Base directory" hint="Optional. Paths in jobs are relative to it.">
              <Input className="font-mono" value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="/srv/drop" />
            </Field>
          </div>
        )}
        {kind === 'agent' && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Agent">
              <Select value={agentId} onChange={(e) => setAgentId(e.target.value)}>
                <option value="">Choose an agent…</option>
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} {a.online ? '' : '(offline)'}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Directory on the agent" hint="A local disk or a share mounted on that machine.">
              <Input className="font-mono" value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="/data/exports" />
            </Field>
          </div>
        )}
        {kind === 's3' && (
          <div className="grid gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Bucket">
                <Input className="font-mono" value={bucket} onChange={(e) => setBucket(e.target.value)} placeholder="acme-reports" />
              </Field>
              <Field label="Key prefix" hint="Optional folder inside the bucket.">
                <Input className="font-mono" value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="incoming" />
              </Field>
              <Field label="Endpoint" hint="Leave empty for AWS; set host:port for MinIO, Ceph, Wasabi…">
                <Input className="font-mono" value={endpoint} onChange={(e) => setEndpoint(e.target.value)} placeholder="minio.internal:9000" />
              </Field>
              <Field label="Region" hint="Required for AWS.">
                <Input className="font-mono" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="eu-west-1" />
              </Field>
              <Field label="Access key" hint="Leave both keys empty to use the server’s IAM role or AWS environment.">
                <Input className="font-mono" value={accessKey} onChange={(e) => setAccessKey(e.target.value)} autoComplete="off" />
              </Field>
              <Field label="Secret key" hint={isEdit && location.has_secret_key ? 'Leave blank to keep the stored key.' : 'Sealed at rest. Never shown again.'}>
                <Input type="password" className="font-mono" value={secretKey} onChange={(e) => setSecretKey(e.target.value)} autoComplete="new-password" />
              </Field>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <Switch label="Path-style URLs (MinIO, Ceph)" checked={pathStyle} onChange={setPathStyle} />
              <Switch label="Plain HTTP endpoint" checked={insecure} onChange={setInsecure} />
            </div>
          </div>
        )}
      </div>
    </Dialog>
  )
}
