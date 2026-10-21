import { NavLink } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { useAgents, useStats } from '@/hooks/queries'
import { navGroups, type NavBadge } from './nav'
import { Logo } from './Logo'
import { UserMenu } from './UserMenu'

export function Sidebar({
  onLogout,
  onShortcuts,
  onNavigate,
  className,
}: {
  onLogout: () => void
  onShortcuts: () => void
  onNavigate?: () => void
  className?: string
}) {
  const { data: stats } = useStats()
  const { data: agents } = useAgents()
  const offline = agents?.filter((a) => !a.online).length ?? 0
  const running = stats?.running ?? 0

  const badge = (b?: NavBadge) => {
    if (b === 'running' && running > 0) {
      return (
        <span className="ml-auto flex items-center gap-1.5 rounded-full bg-accent-soft px-2 py-0.5 text-xs font-bold text-accent">
          <span className="pulse-dot h-1.5 w-1.5 rounded-full bg-accent" aria-hidden="true" />
          {running}
          <span className="sr-only"> running</span>
        </span>
      )
    }
    if (b === 'offline' && offline > 0) {
      return (
        <span className="ml-auto flex items-center gap-1.5 rounded-full bg-danger-soft px-2 py-0.5 text-xs font-bold text-danger">
          <span className="h-1.5 w-1.5 rounded-[1px] bg-danger" aria-hidden="true" />
          {offline}
          <span className="sr-only"> offline</span>
        </span>
      )
    }
    return null
  }

  return (
    <aside className={cn('flex w-[248px] shrink-0 flex-col gap-6 border-r border-line bg-side px-3.5 pt-5 pb-4 text-ink', className)}>
      <NavLink to="/" onClick={onNavigate} className="px-2 text-ink" aria-label="Kairos, go to Today">
        <Logo />
      </NavLink>
      <nav aria-label="Main" className="flex flex-1 flex-col gap-5 overflow-y-auto">
        {navGroups.map((g, gi) => (
          <div key={gi} className="flex flex-col gap-0.5">
            {g.label && <div className="px-3 pb-1.5 text-[13px] text-faint">{g.label}</div>}
            {g.items.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                onClick={onNavigate}
                className={({ isActive }) =>
                  cn(
                    'flex h-10 items-center gap-3 rounded-full px-3.5 text-[14px] transition-colors',
                    isActive
                      ? 'bg-accent-soft font-semibold text-accent'
                      : 'font-medium text-dim hover:bg-raised hover:text-ink',
                  )
                }
              >
                <item.icon size={18} strokeWidth={1.8} aria-hidden="true" className="shrink-0" />
                <span className="truncate">{item.label}</span>
                {badge(item.badge)}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>
      <UserMenu onLogout={onLogout} onShortcuts={onShortcuts} />
    </aside>
  )
}
