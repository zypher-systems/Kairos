import { Component, type ErrorInfo, type ReactNode } from 'react'

// Catches render errors in a page so the shell and navigation stay usable.
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('page crashed', error, info.componentStack)
  }

  componentDidUpdate(prev: { resetKey?: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null })
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <div role="alert" className="mx-auto max-w-lg py-20 text-center">
        <h1 className="font-display text-[24px] font-semibold tracking-[-0.02em] text-ink">Something went wrong</h1>
        <p className="mt-2 text-sm text-faint">This page hit an error. Your jobs keep running.</p>
        <pre className="mt-5 overflow-x-auto rounded-lg bg-raised p-3 text-left font-mono text-xs text-dim">
          {this.state.error.message}
        </pre>
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="mt-5 inline-flex h-10 items-center rounded-md bg-accent px-4 text-sm font-semibold text-accent-fg hover:bg-accent/90"
        >
          Reload the page
        </button>
      </div>
    )
  }
}
