import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Toaster } from 'sonner'

import './index.css'
import AppShell from '@/components/AppShell'
import Login, { useAuthGating } from '@/pages/Login'
import { AuthProvider } from '@/lib/auth'
import { ThemeProvider, useTheme } from '@/lib/theme'
import { ConfirmProvider } from '@/components/confirm'

const qc = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 3000, retry: 1, refetchOnWindowFocus: false },
  },
})

function Gate() {
  const { state, username, role, refresh } = useAuthGating()
  if (state === 'loading') {
    return (
      <div className="flex min-h-full items-center justify-center">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-line border-t-accent" />
      </div>
    )
  }
  if (state === 'setup' || state === 'login') {
    return <Login mode={state} onAuthed={refresh} />
  }
  return (
    <AuthProvider username={username} role={role}>
      <AppShell onLogout={refresh} />
    </AuthProvider>
  )
}

function ThemedToaster() {
  const { resolved } = useTheme()
  return (
    <Toaster
      theme={resolved}
      position="top-right"
      toastOptions={{
        style: {
          background: 'var(--color-panel)',
          border: '1px solid var(--color-line)',
          borderRadius: '12px',
          color: 'var(--color-ink)',
          fontFamily: 'var(--font-sans)',
        },
      }}
    />
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <ConfirmProvider>
          <BrowserRouter>
            <Routes>
              <Route path="*" element={<Gate />} />
            </Routes>
          </BrowserRouter>
        </ConfirmProvider>
        <ThemedToaster />
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
