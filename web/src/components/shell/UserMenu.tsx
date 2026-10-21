import * as DM from '@radix-ui/react-dropdown-menu'
import { BookOpen, Braces, Check, ChevronsUpDown, Keyboard, LifeBuoy, LogOut, Monitor, Moon, Sun } from 'lucide-react'
import { useAuth } from '@/lib/auth'
import { useTheme, type ThemePref } from '@/lib/theme'
import { API_SPEC_URL, DOCS_URL, SUPPORT_URL } from '@/lib/links'

const itemCls =
  'flex h-9 cursor-pointer items-center gap-2.5 rounded-md px-2.5 text-sm text-ink outline-none select-none data-[highlighted]:bg-raised'

const themes: { value: ThemePref; label: string; icon: typeof Sun }[] = [
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'system', label: 'Match system', icon: Monitor },
]

export function UserMenu({ onLogout, onShortcuts }: { onLogout: () => void; onShortcuts: () => void }) {
  const { username, role } = useAuth()
  const { pref, setPref } = useTheme()
  const initial = (username || role || '?').charAt(0).toUpperCase()

  return (
    <DM.Root>
      <DM.Trigger asChild>
        <button
          type="button"
          className="flex w-full items-center gap-2.5 rounded-full border border-line bg-panel p-1.5 pr-3 text-left text-ink transition-colors hover:bg-raised cursor-pointer"
        >
          <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-accent text-[13px] font-bold text-accent-fg">
            {initial}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[13px] font-semibold">{username || 'Signed in'}</span>
            <span className="block truncate text-xs text-faint capitalize">{role}</span>
          </span>
          <ChevronsUpDown size={15} className="shrink-0 text-faint" aria-hidden="true" />
        </button>
      </DM.Trigger>
      <DM.Portal>
        <DM.Content
          side="top"
          align="start"
          sideOffset={8}
          className="anim-in z-50 w-64 rounded-xl border border-line bg-panel p-1.5 shadow-xl"
        >
          <DM.Label className="px-2.5 pt-1.5 pb-1 text-xs font-semibold text-faint">Theme</DM.Label>
          <DM.RadioGroup value={pref} onValueChange={(v) => setPref(v as ThemePref)}>
            {themes.map((t) => (
              <DM.RadioItem key={t.value} value={t.value} className={itemCls}>
                <t.icon size={15} aria-hidden="true" className="text-dim" />
                <span className="flex-1">{t.label}</span>
                <DM.ItemIndicator>
                  <Check size={15} aria-hidden="true" className="text-accent-ink" />
                </DM.ItemIndicator>
              </DM.RadioItem>
            ))}
          </DM.RadioGroup>
          <DM.Separator className="my-1.5 h-px bg-line-soft" />
          {DOCS_URL && (
            <DM.Item asChild className={itemCls}>
              <a href={DOCS_URL} target="_blank" rel="noreferrer">
                <BookOpen size={15} aria-hidden="true" className="text-dim" />
                Documentation
              </a>
            </DM.Item>
          )}
          {SUPPORT_URL && (
            <DM.Item asChild className={itemCls}>
              <a href={SUPPORT_URL} target="_blank" rel="noreferrer">
                <LifeBuoy size={15} aria-hidden="true" className="text-dim" />
                Support
              </a>
            </DM.Item>
          )}
          <DM.Item asChild className={itemCls}>
            <a href={API_SPEC_URL} target="_blank" rel="noreferrer">
              <Braces size={15} aria-hidden="true" className="text-dim" />
              API reference (OpenAPI)
            </a>
          </DM.Item>
          <DM.Item className={itemCls} onSelect={onShortcuts}>
            <Keyboard size={15} aria-hidden="true" className="text-dim" />
            Keyboard shortcuts
          </DM.Item>
          <DM.Separator className="my-1.5 h-px bg-line-soft" />
          <DM.Item className={itemCls} onSelect={onLogout}>
            <LogOut size={15} aria-hidden="true" className="text-dim" />
            Sign out
          </DM.Item>
          <div className="px-2.5 pt-1.5 pb-1 font-mono text-[11px] text-faint">Kairos v{__APP_VERSION__}</div>
        </DM.Content>
      </DM.Portal>
    </DM.Root>
  )
}
