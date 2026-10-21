import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import * as RD from '@radix-ui/react-dialog'
import { Clock3, CornerDownLeft, Moon, Plus, Search, Sun } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useJobs } from '@/hooks/queries'
import { useTheme } from '@/lib/theme'
import { allNavItems } from './nav'

function fmtEvery(sec: number) {
  if (sec % 86400 === 0) return `${sec / 86400}d`
  if (sec % 3600 === 0) return `${sec / 3600}h`
  if (sec % 60 === 0) return `${sec / 60}m`
  return `${sec}s`
}

interface Cmd {
  id: string
  group: 'Go to' | 'Actions' | 'Jobs'
  label: string
  hint?: string
  icon: typeof Search
  keywords?: string
  run: () => void
}

// Command palette: ⌘K / Ctrl+K. Jump to a page or job, or run an action.
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const navigate = useNavigate()
  const { resolved, setPref } = useTheme()
  const { data: jobs } = useJobs()
  const [q, setQ] = useState('')
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)

  const go = (to: string) => () => {
    onOpenChange(false)
    navigate(to)
  }

  const commands = useMemo<Cmd[]>(() => {
    const pages: Cmd[] = allNavItems.map((n) => ({
      id: `nav:${n.to}`,
      group: 'Go to',
      label: n.label,
      icon: n.icon,
      keywords: n.keywords,
      run: go(n.to),
    }))
    const actions: Cmd[] = [
      { id: 'act:new-job', group: 'Actions', label: 'New job', icon: Plus, keywords: 'create add', run: go('/jobs/new') },
      {
        id: 'act:theme',
        group: 'Actions',
        label: resolved === 'dark' ? 'Switch to light theme' : 'Switch to dark theme',
        icon: resolved === 'dark' ? Sun : Moon,
        keywords: 'theme dark light mode appearance',
        run: () => {
          setPref(resolved === 'dark' ? 'light' : 'dark')
          onOpenChange(false)
        },
      },
    ]
    const jobCmds: Cmd[] = (jobs ?? []).map((j) => ({
      id: `job:${j.id}`,
      group: 'Jobs',
      label: j.name,
      hint: j.schedule_type === 'cron' ? j.cron_expr : j.schedule_type === 'interval' ? `every ${fmtEvery(j.interval_seconds)}` : 'once',
      icon: Clock3,
      keywords: (j.tags ?? []).join(' '),
      run: go(`/jobs/${j.id}`),
    }))
    return [...pages, ...actions, ...jobCmds]
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jobs, resolved])

  const results = useMemo(() => {
    const needle = q.trim().toLowerCase()
    if (!needle) return commands.filter((c) => c.group !== 'Jobs').concat(commands.filter((c) => c.group === 'Jobs').slice(0, 5))
    return commands.filter((c) => `${c.label} ${c.keywords ?? ''} ${c.hint ?? ''}`.toLowerCase().includes(needle)).slice(0, 40)
  }, [q, commands])

  useEffect(() => setActive(0), [q, open])
  useEffect(() => {
    if (!open) setQ('')
  }, [open])
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active])

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((a) => Math.min(a + 1, results.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => Math.max(a - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      results[active]?.run()
    }
  }

  let lastGroup = ''
  return (
    <RD.Root open={open} onOpenChange={onOpenChange}>
      <RD.Portal>
        <RD.Overlay className="fixed inset-0 z-50 bg-black/45 backdrop-blur-[2px]" />
        <RD.Content className="anim-in fixed top-[12vh] left-1/2 z-50 w-[calc(100vw-2rem)] max-w-xl -translate-x-1/2 overflow-hidden rounded-2xl border border-line bg-panel shadow-2xl focus:outline-none">
          <RD.Title className="sr-only">Search or jump to</RD.Title>
          <RD.Description className="sr-only">Type to filter pages, actions and jobs. Use the arrow keys and Enter.</RD.Description>
          <div className="flex items-center gap-3 border-b border-line-soft px-4">
            <Search size={17} className="shrink-0 text-faint" aria-hidden="true" />
            <input
              autoFocus
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={onKey}
              role="combobox"
              aria-expanded="true"
              aria-controls="cmdk-list"
              aria-activedescendant={results[active] ? `cmdk-${results[active].id}` : undefined}
              aria-label="Search pages, actions and jobs"
              placeholder="Search or jump to…"
              className="h-14 flex-1 bg-transparent text-[15px] text-ink placeholder:text-faint focus:outline-none"
            />
            <kbd className="rounded border border-line px-1.5 font-mono text-[11px] text-faint">Esc</kbd>
          </div>
          <div id="cmdk-list" ref={listRef} role="listbox" aria-label="Results" className="max-h-[52vh] overflow-y-auto p-2">
            {results.length === 0 && <div className="px-3 py-8 text-center text-sm text-faint">No matches for “{q}”.</div>}
            {results.map((c, i) => {
              const header = c.group !== lastGroup ? c.group : null
              lastGroup = c.group
              return (
                <div key={c.id}>
                  {header && <div className="px-3 pt-2.5 pb-1 text-xs font-semibold text-faint">{header}</div>}
                  <div
                    id={`cmdk-${c.id}`}
                    data-index={i}
                    role="option"
                    aria-selected={i === active}
                    onMouseMove={() => setActive(i)}
                    onClick={() => c.run()}
                    className={cn(
                      'flex h-10 cursor-pointer items-center gap-3 rounded-lg px-3 text-sm',
                      i === active ? 'bg-raised text-ink' : 'text-dim',
                    )}
                  >
                    <c.icon size={16} aria-hidden="true" className="shrink-0 text-faint" />
                    <span className={cn('truncate', c.group === 'Jobs' && 'font-semibold')}>{c.label}</span>
                    {c.hint && <span className="ml-auto truncate font-mono text-xs text-faint">{c.hint}</span>}
                    {i === active && !c.hint && <CornerDownLeft size={14} aria-hidden="true" className="ml-auto text-faint" />}
                  </div>
                </div>
              )
            })}
          </div>
        </RD.Content>
      </RD.Portal>
    </RD.Root>
  )
}
