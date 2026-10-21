import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import * as AD from '@radix-ui/react-alert-dialog'
import { Button } from '@/components/ui'

export interface ConfirmOptions {
  title: string
  body?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
}

type ConfirmFn = (o: ConfirmOptions) => Promise<boolean>

const ConfirmContext = createContext<ConfirmFn | null>(null)

// ConfirmProvider renders one accessible alert dialog for the whole app.
// Usage: `if (!(await confirm({ title: 'Delete job?', destructive: true }))) return`
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [opts, setOpts] = useState<ConfirmOptions | null>(null)
  const resolver = useRef<((v: boolean) => void) | null>(null)

  const confirm = useCallback<ConfirmFn>((o) => {
    resolver.current?.(false)
    setOpts(o)
    return new Promise<boolean>((resolve) => {
      resolver.current = resolve
    })
  }, [])

  const settle = (v: boolean) => {
    resolver.current?.(v)
    resolver.current = null
    setOpts(null)
  }

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <AD.Root open={opts !== null} onOpenChange={(o) => !o && settle(false)}>
        <AD.Portal>
          <AD.Overlay className="fixed inset-0 z-[60] bg-black/50 backdrop-blur-[2px]" />
          <AD.Content className="anim-in fixed top-1/2 left-1/2 z-[60] w-[calc(100vw-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-line bg-panel p-6 shadow-2xl focus:outline-none">
            <AD.Title className="font-display text-[19px] font-semibold tracking-[-0.01em] text-ink">{opts?.title}</AD.Title>
            <AD.Description asChild>
              <div className="mt-2 text-sm leading-relaxed text-faint">{opts?.body ?? 'This cannot be undone.'}</div>
            </AD.Description>
            <div className="mt-6 flex justify-end gap-2">
              <AD.Cancel asChild>
                <Button variant="secondary">{opts?.cancelLabel ?? 'Cancel'}</Button>
              </AD.Cancel>
              <AD.Action asChild>
                <Button variant={opts?.destructive ? 'danger' : 'primary'} onClick={() => settle(true)}>
                  {opts?.confirmLabel ?? (opts?.destructive ? 'Delete' : 'Confirm')}
                </Button>
              </AD.Action>
            </div>
          </AD.Content>
        </AD.Portal>
      </AD.Root>
    </ConfirmContext.Provider>
  )
}

export function useConfirm(): ConfirmFn {
  const ctx = useContext(ConfirmContext)
  if (!ctx) throw new Error('useConfirm outside ConfirmProvider')
  return ctx
}
