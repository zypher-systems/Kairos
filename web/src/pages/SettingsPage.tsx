import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { useAuth } from '@/lib/auth'
import { cn } from '@/lib/utils'
import { AccountSettings, AuditSettings, GeneralSettings, HASettings, IntegrationsSettings, UsersSettings } from './settings/pages'

const tabs: { to: string; label: string; admin?: boolean }[] = [
  { to: 'general', label: 'General' },
  { to: 'account', label: 'Account' },
  { to: 'users', label: 'Users', admin: true },
  { to: 'integrations', label: 'Integrations' },
  { to: 'ha', label: 'High availability' },
  { to: 'audit', label: 'Audit log' },
]

export default function SettingsPage() {
  const { isAdmin } = useAuth()
  const visible = tabs.filter((t) => !t.admin || isAdmin)

  return (
    <div className="anim-in">
      <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Settings</h1>
      <div className="mt-6 grid gap-8 lg:grid-cols-[200px_minmax(0,1fr)]">
        <nav aria-label="Settings" className="-mx-1 overflow-x-auto lg:mx-0 lg:overflow-visible">
          <ul className="flex gap-1 px-1 lg:sticky lg:top-2 lg:flex-col lg:px-0">
            {visible.map((t) => (
              <li key={t.to} className="shrink-0">
                <NavLink
                  to={`/settings/${t.to}`}
                  className={({ isActive }) =>
                    cn(
                      'flex h-9 items-center gap-2 rounded-lg px-3 text-sm whitespace-nowrap transition-colors',
                      isActive ? 'bg-raised font-semibold text-ink' : 'text-dim hover:bg-raised/60 hover:text-ink',
                    )
                  }
                >
                  <span className="flex-1">{t.label}</span>
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="min-w-0">
          <Routes>
            <Route index element={<Navigate to="/settings/general" replace />} />
            <Route path="general" element={<GeneralSettings />} />
            <Route path="account" element={<AccountSettings />} />
            {isAdmin && <Route path="users" element={<UsersSettings />} />}
            <Route path="integrations" element={<IntegrationsSettings />} />
            <Route path="ha" element={<HASettings />} />
            <Route path="audit" element={<AuditSettings />} />
            <Route path="*" element={<Navigate to="/settings/general" replace />} />
          </Routes>
        </div>
      </div>
    </div>
  )
}
