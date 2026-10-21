import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'

export type ThemePref = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'kairos-theme' // also read by public/theme-init.js

function readPref(): ThemePref {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // storage blocked: fall back to the default
  }
  return 'dark'
}

const media = () => window.matchMedia('(prefers-color-scheme: dark)')

interface ThemeState {
  pref: ThemePref
  resolved: 'light' | 'dark'
  setPref: (p: ThemePref) => void
}

const ThemeContext = createContext<ThemeState | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [pref, setPrefState] = useState<ThemePref>(readPref)
  const [systemDark, setSystemDark] = useState(() => media().matches)

  useEffect(() => {
    const m = media()
    const onChange = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    m.addEventListener('change', onChange)
    return () => m.removeEventListener('change', onChange)
  }, [])

  const resolved = pref === 'system' ? (systemDark ? 'dark' : 'light') : pref

  useEffect(() => {
    document.documentElement.classList.toggle('dark', resolved === 'dark')
  }, [resolved])

  const setPref = useCallback((p: ThemePref) => {
    setPrefState(p)
    try {
      localStorage.setItem(STORAGE_KEY, p)
    } catch {
      // not persisted; still applies for this session
    }
  }, [])

  return <ThemeContext.Provider value={{ pref, resolved, setPref }}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeState {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme outside ThemeProvider')
  return ctx
}
