import { Link } from 'react-router-dom'

export default function NotFound() {
  return (
    <div className="mx-auto max-w-lg py-24 text-center">
      <p className="font-mono text-sm text-faint">404</p>
      <h1 className="mt-2 font-display text-[30px] font-semibold tracking-[-0.03em] text-ink">This page doesn't exist</h1>
      <p className="mt-2 text-sm text-faint">The link may be old, or the job or run it pointed to was deleted.</p>
      <Link
        to="/"
        className="mt-6 inline-flex h-10 items-center rounded-md bg-accent px-4 text-sm font-semibold text-accent-fg hover:bg-accent/90"
      >
        Back to Today
      </Link>
    </div>
  )
}
