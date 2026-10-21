import {
  Children,
  cloneElement,
  forwardRef,
  isValidElement,
  useId,
  useRef,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type TextareaHTMLAttributes,
  type SelectHTMLAttributes,
  type HTMLAttributes,
  type KeyboardEvent,
  type LabelHTMLAttributes,
  type ReactElement,
  type ReactNode,
} from 'react'
import * as RD from '@radix-ui/react-dialog'
import * as DM from '@radix-ui/react-dropdown-menu'
import { cva, type VariantProps } from 'class-variance-authority'
import { Link } from 'react-router-dom'
import { MoreHorizontal, X, type LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

// ---------- Button ----------
const buttonVariants = cva(
  [
    'inline-flex shrink-0 items-center justify-center gap-2 rounded-full font-semibold whitespace-nowrap select-none cursor-pointer',
    'transition-colors duration-150 disabled:pointer-events-none disabled:opacity-50',
    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent',
  ],
  {
    variants: {
      variant: {
        primary: 'bg-accent text-accent-fg hover:bg-accent/90',
        // 'outline' is the historical name for the bordered secondary button.
        outline: 'border border-line bg-panel text-ink hover:bg-raised',
        secondary: 'border border-line bg-panel text-ink hover:bg-raised',
        ghost: 'text-dim hover:bg-raised hover:text-ink',
        subtle: 'text-ink hover:bg-raised',
        danger: 'bg-danger text-danger-fg hover:bg-danger/90',
        'danger-ghost': 'text-danger hover:bg-danger-soft',
        link: 'h-auto px-0 text-accent-ink underline-offset-4 hover:underline',
      },
      size: {
        sm: 'h-8 px-3.5 text-[13px]',
        md: 'h-10 px-[18px] text-sm',
        lg: 'h-11 px-5 text-[15px]',
        icon: 'h-9 w-9',
      },
    },
    compoundVariants: [{ variant: 'link', className: 'h-auto px-0' }],
    defaultVariants: { variant: 'subtle', size: 'md' },
  },
)

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, type, ...props }, ref) => (
  <button ref={ref} type={type ?? 'button'} className={cn(buttonVariants({ variant, size }), className)} {...props} />
))
Button.displayName = 'Button'

// A link that looks like a button, for actions that navigate.
export function LinkButton({
  to,
  variant = 'secondary',
  size,
  className,
  children,
}: { to: string; className?: string; children: ReactNode } & VariantProps<typeof buttonVariants>) {
  return (
    <Link to={to} className={cn(buttonVariants({ variant, size }), className)}>
      {children}
    </Link>
  )
}

// ---------- Row actions ----------
// Every list row follows one rule: what you do often is a pill button; Edit
// and anything destructive sit in this menu.
export function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  return (
    <DM.Root>
      <DM.Trigger asChild>
        <Button variant="ghost" size="icon" aria-label={label} className="h-8 w-8">
          <MoreHorizontal size={17} />
        </Button>
      </DM.Trigger>
      <DM.Portal>
        <DM.Content align="end" sideOffset={6} className="anim-in z-50 min-w-44 rounded-lg border border-line bg-panel p-1.5 text-ink shadow-xl">
          {children}
        </DM.Content>
      </DM.Portal>
    </DM.Root>
  )
}

export function MenuAction({
  danger,
  disabled,
  onSelect,
  children,
}: {
  danger?: boolean
  disabled?: boolean
  onSelect: () => void
  children: ReactNode
}) {
  return (
    <DM.Item
      disabled={disabled}
      onSelect={() => onSelect()}
      className={cn(
        'flex h-9 cursor-pointer items-center rounded-md px-2.5 text-sm outline-none select-none data-[disabled]:cursor-default data-[disabled]:opacity-50 data-[highlighted]:bg-raised',
        danger && 'text-danger',
      )}
    >
      {children}
    </DM.Item>
  )
}

// ---------- Inputs ----------
const controlBase = [
  'w-full rounded-md border border-input/70 bg-panel text-sm text-ink transition-colors',
  'placeholder:text-faint hover:border-input',
  'focus:border-accent focus:outline-none focus:ring-3 focus:ring-accent/20',
  'disabled:cursor-not-allowed disabled:opacity-60',
  'aria-[invalid=true]:border-danger aria-[invalid=true]:focus:ring-danger/20',
]

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(({ className, ...props }, ref) => (
  <input ref={ref} className={cn(controlBase, 'h-10 px-3', className)} {...props} />
))
Input.displayName = 'Input'

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => (
    <textarea ref={ref} className={cn(controlBase, 'px-3 py-2 leading-relaxed', className)} {...props} />
  ),
)
Textarea.displayName = 'Textarea'

// Select wraps a native select; size it with wrapperClassName (the wrapper
// is full width by default).
export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement> & { wrapperClassName?: string }>(
  ({ className, wrapperClassName, children, ...props }, ref) => (
    <div className={cn('relative w-full', wrapperClassName)}>
      <select ref={ref} className={cn(controlBase, 'h-10 appearance-none pr-9 pl-3', className)} {...props}>
        {children}
      </select>
      <svg
        aria-hidden="true"
        viewBox="0 0 24 24"
        className="pointer-events-none absolute top-1/2 right-3 h-4 w-4 -translate-y-1/2 text-faint"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
      >
        <path d="m6 9 6 6 6-6" />
      </svg>
    </div>
  ),
)
Select.displayName = 'Select'

export function Label({ className, ...props }: LabelHTMLAttributes<HTMLLabelElement>) {
  return <label className={cn('mb-1.5 block text-[13px] font-semibold text-dim', className)} {...props} />
}

// Field ties its label to the control: a single child element gets an id
// and the label's htmlFor; anything else is wrapped as a labelled group.
export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: ReactNode
  hint?: ReactNode
  error?: ReactNode
  children: ReactNode
}) {
  const auto = useId()
  const hintId = `${auto}-hint`
  const describedBy = error || hint ? hintId : undefined
  const only = Children.count(children) === 1 && isValidElement(children) ? (children as ReactElement<Record<string, unknown>>) : null
  const labelable = only && typeof only.type !== 'string' ? true : only && ['input', 'select', 'textarea'].includes(only.type as string)

  if (only && labelable) {
    const id = (only.props.id as string | undefined) ?? auto
    return (
      <div>
        <Label htmlFor={id}>{label}</Label>
        {cloneElement(only, {
          id,
          'aria-describedby': describedBy,
          'aria-invalid': error ? true : undefined,
        })}
        <FieldNote id={hintId} hint={hint} error={error} />
      </div>
    )
  }
  const labelId = `${auto}-label`
  return (
    <div role="group" aria-labelledby={labelId} aria-describedby={describedBy}>
      <div id={labelId} className="mb-1.5 block text-[13px] font-semibold text-dim">
        {label}
      </div>
      {children}
      <FieldNote id={hintId} hint={hint} error={error} />
    </div>
  )
}

function FieldNote({ id, hint, error }: { id: string; hint?: ReactNode; error?: ReactNode }) {
  if (error) {
    return (
      <p id={id} className="mt-1.5 text-xs font-medium text-danger">
        {error}
      </p>
    )
  }
  if (hint) {
    return (
      <p id={id} className="mt-1.5 text-xs text-faint">
        {hint}
      </p>
    )
  }
  return null
}

// ---------- Switch ----------
export function Switch({
  checked,
  onChange,
  disabled,
  label,
  id,
}: {
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  label?: string
  id?: string
}) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative inline-flex h-6 w-10 shrink-0 items-center rounded-full p-[3px] transition-colors duration-200 cursor-pointer',
        'disabled:cursor-not-allowed disabled:opacity-50',
        checked ? 'bg-accent' : 'bg-input',
      )}
    >
      <span
        className={cn(
          'h-[18px] w-[18px] rounded-full transition-transform duration-200',
          checked ? 'translate-x-4 bg-accent-fg' : 'bg-panel',
        )}
      />
    </button>
  )
}

// ---------- Page structure ----------
export function PageHeader({ title, sub, right }: { title: ReactNode; sub?: ReactNode; right?: ReactNode }) {
  return (
    <header className="flex flex-wrap items-start justify-between gap-4">
      <div className="min-w-0">
        <h1 className="font-display text-[26px] leading-[1.15] font-semibold tracking-[-0.03em] text-ink md:text-[30px]">{title}</h1>
        {sub && <p className="mt-1.5 text-sm text-faint">{sub}</p>}
      </div>
      {right && <div className="flex shrink-0 flex-wrap items-center gap-2.5">{right}</div>}
    </header>
  )
}

// A titled card. `index` is accepted for older call sites and ignored.
export function Section({
  title,
  right,
  className,
  id,
  children,
}: {
  index?: string
  title: ReactNode
  right?: ReactNode
  className?: string
  id?: string
  children: ReactNode
}) {
  return (
    <section id={id} className={cn('rounded-xl border border-line bg-panel', className)}>
      <div className="flex items-center justify-between gap-3 border-b border-line-soft px-5 py-3.5">
        <h2 className="font-display text-[17px] font-semibold tracking-[-0.01em] text-ink">{title}</h2>
        {right}
      </div>
      <div className="p-5">{children}</div>
    </section>
  )
}

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('rounded-xl border border-line bg-panel', className)} {...props} />
}

export function CardHeader({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('flex items-center justify-between gap-3 px-5 pt-4 pb-3', className)} {...props} />
}

export function EmptyState({
  icon: Icon,
  title,
  sub,
  action,
}: {
  icon: LucideIcon
  title: string
  sub?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="px-5 py-14 text-center">
      <span className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-raised text-dim">
        <Icon size={22} strokeWidth={1.8} aria-hidden="true" />
      </span>
      <div className="font-display text-[17px] font-semibold text-ink">{title}</div>
      {sub && <div className="mx-auto mt-1.5 max-w-md text-sm text-faint">{sub}</div>}
      {action && <div className="mt-5 flex justify-center gap-2">{action}</div>}
    </div>
  )
}

export function QueryError({ error }: { error: unknown }) {
  const msg = error instanceof Error ? error.message : 'Request failed'
  return (
    <div role="alert" className="rounded-lg border border-danger/30 bg-danger-soft px-4 py-3 text-sm text-danger">
      {msg}
    </div>
  )
}

// ---------- Tag / status ----------
export function Badge({ className, ...props }: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full bg-raised px-2.5 py-0.5 text-xs font-medium text-dim',
        className,
      )}
      {...props}
    />
  )
}

// Status is carried by the label and the dot shape (round = fine or live,
// square = needs attention), never by colour alone.
const statusStyles: Record<string, { text: string; dot: string; bar: string; pill: string; label: string }> = {
  succeeded: { text: 'text-ok', dot: 'bg-ok rounded-full', bar: 'bg-ok', pill: 'bg-ok-soft text-ok', label: 'Succeeded' },
  queued: { text: 'text-warn', dot: 'bg-warn rounded-full', bar: 'bg-warn/70', pill: 'bg-warn-soft text-warn', label: 'Queued' },
  pending_approval: {
    text: 'text-warn',
    dot: 'bg-warn rounded-[3px] pulse-dot',
    bar: 'bg-warn/70',
    pill: 'bg-warn-soft text-warn',
    label: 'Awaiting approval',
  },
  claimed: { text: 'text-warn', dot: 'bg-warn rounded-full pulse-dot', bar: 'bg-warn/70', pill: 'bg-warn-soft text-warn', label: 'Starting' },
  running: { text: 'text-cyan', dot: 'bg-cyan rounded-full pulse-dot', bar: 'bg-cyan', pill: 'bg-cyan-soft text-cyan-ink', label: 'Running' },
  failed: { text: 'text-danger', dot: 'bg-danger rounded-[3px]', bar: 'bg-danger', pill: 'bg-danger-soft text-danger', label: 'Failed' },
  canceled: { text: 'text-faint', dot: 'bg-faint rounded-[3px]', bar: 'bg-faint/60', pill: 'bg-raised text-faint', label: 'Canceled' },
  skipped: { text: 'text-faint', dot: 'bg-faint rounded-[3px]', bar: 'bg-faint/60', pill: 'bg-raised text-faint', label: 'Skipped' },
}

export function statusColor(status: string) {
  return statusStyles[status] ?? statusStyles.queued
}

// StatusDot is the shape alone, for rows that print the label elsewhere.
export function StatusDot({ status, className }: { status: string; className?: string }) {
  return <span className={cn('h-3 w-3 shrink-0', statusColor(status).dot, className)} aria-hidden="true" />
}

// The one way a run status is shown anywhere: its shape, then its name.
export function StatusBadge({ status }: { status: string }) {
  const s = statusColor(status)
  return (
    <span className={cn('inline-flex items-center gap-2 text-sm font-semibold whitespace-nowrap', s.text)}>
      <StatusDot status={status} />
      {s.label}
    </span>
  )
}

// ---------- Dialog ----------
// Radix gives focus trap, Escape, aria-modal and focus return.
export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  width = 'max-w-lg',
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
  width?: string
}) {
  return (
    <RD.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <RD.Portal>
        <RD.Overlay className="fixed inset-0 z-50 bg-black/50 backdrop-blur-[2px]" />
        <RD.Content
          className={cn(
            'anim-in fixed top-1/2 left-1/2 z-50 flex max-h-[90vh] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col',
            'rounded-2xl border border-line bg-panel shadow-2xl focus:outline-none',
            width,
          )}
        >
          <div className="flex items-start justify-between gap-3 px-6 pt-5 pb-3">
            <div className="min-w-0">
              <RD.Title className="font-display text-[19px] font-semibold tracking-[-0.01em] text-ink">{title}</RD.Title>
              {description ? (
                <RD.Description className="mt-1 text-sm text-faint">{description}</RD.Description>
              ) : (
                <RD.Description className="sr-only">{typeof title === 'string' ? title : 'Dialog'}</RD.Description>
              )}
            </div>
            <RD.Close asChild>
              <Button variant="ghost" size="icon" aria-label="Close" className="-mr-2 h-8 w-8">
                <X size={16} />
              </Button>
            </RD.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-5">{children}</div>
          {footer && (
            <div className="flex flex-wrap justify-end gap-2 rounded-b-2xl border-t border-line-soft bg-bg/60 px-6 py-3.5">
              {footer}
            </div>
          )}
        </RD.Content>
      </RD.Portal>
    </RD.Root>
  )
}

// ---------- Drawer (large window for dense content) ----------
export function Drawer({
  open,
  onClose,
  title,
  sub,
  children,
  footer,
  width = 'max-w-3xl',
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  sub?: ReactNode
  children: ReactNode
  footer?: ReactNode
  width?: string
}) {
  return (
    <RD.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <RD.Portal>
        <RD.Overlay className="fixed inset-0 z-50 bg-black/50 backdrop-blur-[2px]" />
        <RD.Content
          className={cn(
            'anim-in fixed top-1/2 left-1/2 z-50 flex h-[92vh] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col',
            'rounded-2xl border border-line bg-panel shadow-2xl focus:outline-none',
            width,
          )}
        >
          <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line-soft px-6 py-4">
            <div className="min-w-0">
              <RD.Title className="truncate font-display text-[19px] font-semibold text-ink">{title}</RD.Title>
              <RD.Description className={sub ? 'mt-0.5 truncate text-[13px] text-faint' : 'sr-only'}>
                {sub ?? (typeof title === 'string' ? title : 'Editor')}
              </RD.Description>
            </div>
            <RD.Close asChild>
              <Button variant="ghost" size="icon" aria-label="Close">
                <X size={16} />
              </Button>
            </RD.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">{children}</div>
          {footer && (
            <div className="flex shrink-0 items-center justify-end gap-2 rounded-b-2xl border-t border-line-soft bg-bg/60 px-6 py-3.5">
              {footer}
            </div>
          )}
        </RD.Content>
      </RD.Portal>
    </RD.Root>
  )
}

// ---------- Tabs ----------
// Tab strip with roving focus (arrow keys, Home/End).
export function Tabs({
  tabs,
  value,
  onChange,
  label,
}: {
  tabs: { value: string; label: ReactNode }[]
  value: string
  onChange: (v: string) => void
  label?: string
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const onKey = (e: KeyboardEvent, i: number) => {
    let next = -1
    if (e.key === 'ArrowRight') next = (i + 1) % tabs.length
    if (e.key === 'ArrowLeft') next = (i - 1 + tabs.length) % tabs.length
    if (e.key === 'Home') next = 0
    if (e.key === 'End') next = tabs.length - 1
    if (next < 0) return
    e.preventDefault()
    onChange(tabs[next].value)
    refs.current[next]?.focus()
  }
  return (
    <div role="tablist" aria-label={label} className="inline-flex flex-wrap items-center gap-1.5">
      {tabs.map((t, i) => {
        const selected = value === t.value
        return (
          <button
            key={t.value}
            ref={(el) => {
              refs.current[i] = el
            }}
            type="button"
            role="tab"
            aria-selected={selected}
            tabIndex={selected ? 0 : -1}
            onKeyDown={(e) => onKey(e, i)}
            onClick={() => onChange(t.value)}
            className={cn(
              'h-9 rounded-full border px-4 text-[13px] font-semibold transition-colors cursor-pointer',
              selected ? 'border-ink bg-ink text-bg' : 'border-line bg-panel text-ink hover:bg-raised',
            )}
          >
            {t.label}
          </button>
        )
      })}
    </div>
  )
}

// ---------- Spinner ----------
export function Spinner({ className, label = 'Loading' }: { className?: string; label?: string }) {
  return (
    <span role="status" aria-label={label} className="inline-flex">
      <span className={cn('h-4 w-4 animate-spin rounded-full border-2 border-line border-t-accent', className)} />
    </span>
  )
}
