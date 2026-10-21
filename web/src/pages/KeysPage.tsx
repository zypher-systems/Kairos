import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Copy, KeyRound, Plus } from 'lucide-react'
import { api } from '@/lib/api'
import { Button, Input, Field, Dialog, EmptyState, Textarea, RowMenu, MenuAction } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { timeAgo } from '@/lib/utils'
import type { SSHKey } from '@/lib/types'
import { WriteGate } from '@/lib/auth'

export default function KeysPage() {
  const confirm = useConfirm()
  const [keys, setKeys] = useState<SSHKey[]>()
  const [generating, setGenerating] = useState(false)
  const [importing, setImporting] = useState(false)

  const reload = useCallback(() => {
    api.listSSHKeys().then((r) => setKeys(r.keys)).catch(() => undefined)
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const remove = (k: SSHKey) => {
    if (k.hosts_using > 0) {
      toast.error(`${k.name} is used by ${k.hosts_using} ${k.hosts_using === 1 ? 'host' : 'hosts'}. Switch them to another key first.`)
      return
    }
    confirm({
      title: `Delete key ${k.name}?`,
      body: 'The private key is deleted from Kairos. Hosts that trust its public key are not changed.',
      confirmLabel: 'Delete key',
      destructive: true,
    }).then(
      (ok) =>
        void (ok &&
          api
            .deleteSSHKey(k.id)
            .then(() => {
              toast.success(`${k.name} deleted`)
              reload()
            })
            .catch((e) => toast.error(String(e)))),
    )
  }

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">SSH keys</h1>
          <p className="mt-1.5 text-sm text-faint">Keys Kairos uses to sign in to your SSH hosts. Private keys are stored encrypted and never shown.</p>
        </div>
        <WriteGate>
          <Button variant="secondary" onClick={() => setImporting(true)}>
            Import a key
          </Button>
          <Button variant="primary" onClick={() => setGenerating(true)}>
            <Plus size={16} strokeWidth={2.4} /> Generate a key pair
          </Button>
        </WriteGate>
      </header>

      {keys && keys.length === 0 ? (
        <div className="rounded-2xl border border-line bg-panel">
          <EmptyState
            icon={KeyRound}
            title="No keys yet"
            sub="Generate a key pair in Kairos, or import a private key you already use. Then pick it when you add or edit an SSH host."
            action={
              <WriteGate>
                <Button variant="secondary" onClick={() => setImporting(true)}>
                  Import a key
                </Button>
                <Button variant="primary" onClick={() => setGenerating(true)}>
                  Generate a key pair
                </Button>
              </WriteGate>
            }
          />
        </div>
      ) : (
        keys && (
          <div className="overflow-x-auto rounded-2xl border border-line bg-panel">
            <table className="w-full min-w-[640px] text-sm">
              <thead>
                <tr className="border-b border-line-soft text-left text-xs text-faint">
                  <th className="px-5 py-3 font-semibold">Name</th>
                  <th className="px-3 py-3 font-semibold">Used by</th>
                  <th className="px-3 py-3 font-semibold">Created</th>
                  <th className="px-5 py-3">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr key={k.id} className="border-b border-line-soft last:border-b-0">
                    <td className="px-5 py-3">
                      <div className="text-[15px] font-semibold text-ink">{k.name}</div>
                      <div className="mt-0.5 max-w-md truncate font-mono text-xs text-faint" title={k.fingerprint}>
                        {k.fingerprint}
                      </div>
                    </td>
                    <td className="px-3 py-3 text-dim">
                      {k.hosts_using === 0 ? 'No hosts' : `${k.hosts_using} ${k.hosts_using === 1 ? 'host' : 'hosts'}`}
                    </td>
                    <td className="px-3 py-3 whitespace-nowrap text-dim">{timeAgo(k.created_at)}</td>
                    <td className="px-5 py-3">
                      <div className="flex justify-end gap-2">
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => {
                            void navigator.clipboard.writeText(k.public_key)
                            toast.success('Public key copied')
                          }}
                        >
                          <Copy size={14} /> Copy public key
                        </Button>
                        <WriteGate>
                          <RowMenu label={`Actions for ${k.name}`}>
                            <MenuAction danger onSelect={() => remove(k)}>
                              Delete…
                            </MenuAction>
                          </RowMenu>
                        </WriteGate>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}

      <GenerateKeyDialog open={generating} onClose={() => setGenerating(false)} onDone={reload} />
      <ImportKeyDialog open={importing} onClose={() => setImporting(false)} onDone={reload} />
    </div>
  )
}

function GenerateKeyDialog({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState('')

  const create = () => {
    api
      .createSSHKey(name, { generate: true })
      .then(() => {
        toast.success(`Key pair "${name}" generated`)
        setName('')
        onClose()
        onDone()
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Generate a key pair"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" disabled={!name.trim()} onClick={create}>
            Generate
          </Button>
        </>
      }
    >
      <Field label="Name" hint="Kairos creates an Ed25519 key pair. The private key stays on this server, encrypted.">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="prod-deploy" autoFocus />
      </Field>
    </Dialog>
  )
}

function ImportKeyDialog({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState('')
  const [pem, setPem] = useState('')

  const create = () => {
    api
      .createSSHKey(name, { private_key: pem })
      .then(() => {
        toast.success(`Key "${name}" imported`)
        setName('')
        setPem('')
        onClose()
        onDone()
      })
      .catch((e) => toast.error(String(e instanceof Error ? e.message : e)))
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Import a private key"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" disabled={!name.trim() || !pem.trim()} onClick={create}>
            Import
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="existing-deploy-key" autoFocus />
        </Field>
        <Field label="Private key" hint="An OpenSSH or PKCS#8 key without a passphrase. It is stored encrypted and never shown again.">
          <Textarea
            rows={6}
            spellCheck={false}
            className="font-mono text-[12.5px]"
            value={pem}
            onChange={(e) => setPem(e.target.value)}
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          />
        </Field>
      </div>
    </Dialog>
  )
}
