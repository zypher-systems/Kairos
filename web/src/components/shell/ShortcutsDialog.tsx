import { Dialog } from '@/components/ui'

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
const mod = isMac ? '⌘' : 'Ctrl'

const rows: [string, string[]][] = [
  ['Search or jump to', [mod, 'K']],
  ['New job', ['N']],
  ['Go to Today', ['G', 'T']],
  ['Go to Jobs', ['G', 'J']],
  ['Go to Runs', ['G', 'R']],
  ['Show this list', ['?']],
]

export function ShortcutsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onClose={onClose} title="Keyboard shortcuts">
      <dl className="divide-y divide-line-soft">
        {rows.map(([label, keys]) => (
          <div key={label} className="flex items-center justify-between py-2.5 text-sm">
            <dt className="text-dim">{label}</dt>
            <dd className="flex gap-1">
              {keys.map((k) => (
                <kbd key={k} className="min-w-6 rounded-md border border-line bg-raised px-1.5 py-0.5 text-center font-mono text-xs text-ink">
                  {k}
                </kbd>
              ))}
            </dd>
          </div>
        ))}
      </dl>
    </Dialog>
  )
}
