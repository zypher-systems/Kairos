import { createContext, useContext, type ReactNode } from 'react'

export type Role = 'admin' | 'operator' | 'viewer'

export function roleRank(role: string): number {
  switch (role) {
    case 'admin':
      return 2
    case 'operator':
      return 1
    default:
      return 0
  }
}

export type AuthValue = {
  username: string
  role: string
  canWrite: boolean
  isAdmin: boolean
}

const AuthContext = createContext<AuthValue>({
  username: '',
  role: 'viewer',
  canWrite: false,
  isAdmin: false,
})

export function AuthProvider({
  username,
  role,
  children,
}: {
  username: string
  role: string
  children: ReactNode
}) {
  const value: AuthValue = {
    username,
    role,
    canWrite: roleRank(role) >= 1,
    isAdmin: role === 'admin',
  }
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  return useContext(AuthContext)
}

export function WriteGate({ children }: { children: ReactNode }) {
  const { canWrite } = useAuth()
  if (!canWrite) return null
  return <>{children}</>
}

export function AdminGate({ children }: { children: ReactNode }) {
  const { isAdmin } = useAuth()
  if (!isAdmin) return null
  return <>{children}</>
}
