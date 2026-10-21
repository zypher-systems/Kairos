import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { Routes, Route, useLocation, useNavigate } from 'react-router-dom'
import * as RD from '@radix-ui/react-dialog'
import { Menu, Moon, Search, Sun, X } from 'lucide-react'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { Button, Spinner } from '@/components/ui'
import { Sidebar } from '@/components/shell/Sidebar'
import { Logo } from '@/components/shell/Logo'
import { useTheme } from '@/lib/theme'

// Loaded on first open.
const CommandPalette = lazy(() => import('@/components/shell/CommandPalette').then((m) => ({ default: m.CommandPalette })))
const ShortcutsDialog = lazy(() => import('@/components/shell/ShortcutsDialog').then((m) => ({ default: m.ShortcutsDialog })))
const shortcutModifier = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl'

// Pages load on demand so the first paint only ships the shell.
const Dashboard = lazy(() => import('@/pages/Dashboard'))
const SchedulePage = lazy(() => import('@/pages/SchedulePage'))
const JobsPage = lazy(() => import('@/pages/JobsPage'))
const JobEditor = lazy(() => import('@/pages/JobEditor'))
const RunsPage = lazy(() => import('@/pages/RunsPage'))
const RunDetail = lazy(() => import('@/pages/RunDetail'))
const FilesPage = lazy(() => import('@/pages/FilesPage'))
const StoragePage = lazy(() => import('@/pages/StoragePage'))
const KeysPage = lazy(() => import('@/pages/KeysPage'))
const NotificationsPage = lazy(() => import('@/pages/NotificationsPage'))
const WorkflowsPage = lazy(() => import('@/pages/WorkflowsPage'))
const TargetsPage = lazy(() => import('@/pages/TargetsPage'))
const SettingsPage = lazy(() => import('@/pages/SettingsPage'))
const NotFound = lazy(() => import('@/pages/NotFound'))

function typingIn(el: EventTarget | null) {
  const t = el as HTMLElement | null
  return !!t && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName))
}

export default function AppShell({ onLogout }: { onLogout: () => void }) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { resolved, setPref } = useTheme()
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [shortcutsOpen, setShortcutsOpen] = useState(false)
  const [mobileNav, setMobileNav] = useState(false)
  const pendingG = useRef(0)

  const logout = () => {
    fetch('/api/v1/auth/logout', { method: 'POST' }).finally(() => onLogout())
  }

  // Global shortcuts: Cmd/Ctrl+K palette, N new job, G then T/J/R, ? help.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen((o) => !o)
        return
      }
      if (e.metaKey || e.ctrlKey || e.altKey || typingIn(e.target) || document.querySelector('[role="dialog"]')) return
      const k = e.key.toLowerCase()
      if (Date.now() - pendingG.current < 1000) {
        pendingG.current = 0
        const to = { t: '/', j: '/jobs', r: '/runs' }[k]
        if (to) navigate(to)
        return
      }
      if (k === 'g') pendingG.current = Date.now()
      else if (k === 'n') navigate('/jobs/new')
      else if (e.key === '?') setShortcutsOpen(true)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [navigate])

  return (
    <div className="flex h-full">
      <Sidebar className="hidden md:flex" onLogout={logout} onShortcuts={() => setShortcutsOpen(true)} />

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-16 shrink-0 items-center gap-3 px-5 md:px-9">
          <Button variant="ghost" size="icon" className="md:hidden" aria-label="Open menu" onClick={() => setMobileNav(true)}>
            <Menu size={20} />
          </Button>
          <span className="text-ink md:hidden">
            <Logo />
          </span>
          <div className="flex-1" />
          <button
            type="button"
            onClick={() => setPaletteOpen(true)}
            className="flex h-10 items-center gap-2.5 rounded-full border border-line bg-panel px-4 text-sm text-faint transition-colors hover:border-input md:w-72 cursor-pointer"
          >
            <Search size={16} aria-hidden="true" />
            <span className="hidden flex-1 text-left md:inline">Search or jump to…</span>
            <span className="sr-only md:hidden">Search or jump to</span>
            <kbd className="hidden rounded-full bg-raised px-2 py-px font-sans text-[11px] font-medium text-dim md:inline">{shortcutModifier}K</kbd>
          </button>
          <Button
            variant="secondary"
            size="icon"
            className="h-10 w-10"
            aria-label={resolved === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
            onClick={() => setPref(resolved === 'dark' ? 'light' : 'dark')}
          >
            {resolved === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
          </Button>
        </header>

        <main className="min-w-0 flex-1 overflow-y-auto">
          <div className="mx-auto h-full max-w-[1400px] px-5 pt-2 pb-10 md:px-9">
          <ErrorBoundary resetKey={pathname}>
          <Suspense fallback={<div className="flex justify-center py-20"><Spinner /></div>}>
          <Routes>
            <Route index element={<Dashboard />} />
            <Route path="schedule" element={<SchedulePage />} />
            <Route path="jobs" element={<JobsPage />} />
            <Route path="jobs/new" element={<JobEditor key="new" />} />
            <Route path="jobs/:id" element={<JobEditor />} />
            <Route path="runs" element={<RunsPage />} />
            <Route path="runs/:id" element={<RunDetail />} />
            <Route path="files" element={<FilesPage />} />
            <Route path="storage" element={<StoragePage />} />
            <Route path="targets" element={<TargetsPage />} />
            <Route path="keys" element={<KeysPage />} />
            <Route path="notifications" element={<NotificationsPage />} />
            <Route path="workflows" element={<WorkflowsPage />} />
            <Route path="settings/*" element={<SettingsPage />} />
            <Route path="*" element={<NotFound />} />
          </Routes>
          </Suspense>
          </ErrorBoundary>
          </div>
        </main>
      </div>

      <RD.Root open={mobileNav} onOpenChange={setMobileNav}>
        <RD.Portal>
          <RD.Overlay className="fixed inset-0 z-50 bg-black/50 md:hidden" />
          <RD.Content className="fixed inset-y-0 left-0 z-50 flex md:hidden focus:outline-none">
            <RD.Title className="sr-only">Menu</RD.Title>
            <RD.Description className="sr-only">Main navigation</RD.Description>
            <Sidebar
              className="h-full shadow-2xl"
              onLogout={logout}
              onShortcuts={() => {
                setMobileNav(false)
                setShortcutsOpen(true)
              }}
              onNavigate={() => setMobileNav(false)}
            />
            <RD.Close asChild>
              <button type="button" aria-label="Close menu" className="m-3 flex h-10 w-10 items-center justify-center rounded-full bg-panel text-ink shadow">
                <X size={18} />
              </button>
            </RD.Close>
          </RD.Content>
        </RD.Portal>
      </RD.Root>

      <Suspense fallback={null}>
        {paletteOpen && <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />}
        {shortcutsOpen && <ShortcutsDialog open={shortcutsOpen} onClose={() => setShortcutsOpen(false)} />}
      </Suspense>
    </div>
  )
}
