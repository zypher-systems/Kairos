import { Activity, Bell, CalendarRange, Clock3, FolderOpen, HardDrive, House, KeyRound, Server, SlidersHorizontal, Workflow, type LucideIcon } from 'lucide-react'

export type NavBadge = 'running' | 'offline'

export interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  end?: boolean
  badge?: NavBadge
  keywords?: string
}

export const navGroups: { label?: string; items: NavItem[] }[] = [
  {
    items: [
      { to: '/', label: 'Today', icon: House, end: true, keywords: 'dashboard overview home' },
      { to: '/jobs', label: 'Jobs', icon: Clock3, keywords: 'cron schedule tasks' },
      { to: '/workflows', label: 'Workflows', icon: Workflow, keywords: 'dag chain pipeline' },
      { to: '/runs', label: 'Runs', icon: Activity, badge: 'running', keywords: 'history logs executions' },
      { to: '/schedule', label: 'Schedule', icon: CalendarRange, keywords: 'timeline upcoming' },
    ],
  },
  {
    label: 'Where jobs run',
    items: [
      { to: '/targets', label: 'Hosts & agents', icon: Server, badge: 'offline', keywords: 'targets ssh agent servers machines' },
      { to: '/keys', label: 'SSH keys', icon: KeyRound, keywords: 'credentials' },
      { to: '/storage', label: 'Storage', icon: HardDrive, keywords: 'locations nfs smb mount sftp s3 bucket transfer' },
      { to: '/files', label: 'Files', icon: FolderOpen, keywords: 'library uploads transfer' },
    ],
  },
  {
    label: 'Workspace',
    items: [
      { to: '/notifications', label: 'Alerts', icon: Bell, keywords: 'notifications channels slack webhook email' },
      { to: '/settings', label: 'Settings', icon: SlidersHorizontal, keywords: 'users tokens sso audit' },
    ],
  },
]

export const allNavItems = navGroups.flatMap((g) => g.items)
