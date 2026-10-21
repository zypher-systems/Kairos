// The Kairos mark: a clock face with the accent hand on "now".
export function Logo({ className }: { className?: string }) {
  return (
    <span className={className} style={{ display: 'inline-flex', alignItems: 'center', gap: 10 }}>
      <svg width="28" height="28" viewBox="0 0 28 28" fill="none" aria-hidden="true">
        <circle cx="14" cy="14" r="12" stroke="currentColor" strokeWidth="2.4" />
        <path d="M14 14 20.5 9" className="stroke-accent" strokeWidth="2.6" strokeLinecap="round" />
        <circle cx="14" cy="14" r="2.2" fill="currentColor" />
      </svg>
      <span className="font-display text-[18px] font-semibold tracking-[-0.02em]">kairos</span>
    </span>
  )
}
