import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { FileUp, PackageOpen } from 'lucide-react'
import { api } from '@/lib/api'
import { Button, Drawer, EmptyState, RowMenu, MenuAction } from '@/components/ui'
import { useConfirm } from '@/components/confirm'
import { cn, fmtBytes, fmtDateTime } from '@/lib/utils'
import type { FileEntry } from '@/lib/types'
import { WriteGate, useAuth } from '@/lib/auth'

const maxViewBytes = 2 << 20

export default function FilesPage() {
  const confirm = useConfirm()
  const { canWrite } = useAuth()
  const [files, setFiles] = useState<FileEntry[]>()
  const [dragging, setDragging] = useState(false)
  const [uploads, setUploads] = useState<{ name: string; pct: number }[]>([])
  const [viewing, setViewing] = useState<FileEntry | null>(null)
  const [viewText, setViewText] = useState<string>()
  const [viewError, setViewError] = useState<string>()
  const [viewLoading, setViewLoading] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const reload = useCallback(() => {
    api
      .listFiles()
      .then((r) =>
        setFiles(
          [...r.files].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()),
        ),
      )
      .catch(() => undefined)
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const upload = useCallback(
    (list: FileList | null) => {
      if (!list || list.length === 0) return
      for (const f of Array.from(list)) {
        setUploads((u) => [...u, { name: f.name, pct: 0 }])
        api
          .uploadFile(f, (pct) =>
            setUploads((u) => u.map((x) => (x.name === f.name ? { ...x, pct } : x))),
          )
          .then(() => {
            toast.success(`${f.name} uploaded`)
            setUploads((u) => u.filter((x) => x.name !== f.name))
            reload()
          })
          .catch((e) => {
            toast.error(String(e instanceof Error ? e.message : e))
            setUploads((u) => u.filter((x) => x.name !== f.name))
          })
      }
    },
    [reload],
  )

  const openView = useCallback((f: FileEntry) => {
    setViewing(f)
    setViewText(undefined)
    setViewError(undefined)
    if (f.size_bytes > maxViewBytes) {
      setViewError('File is too large to view (2 MiB max). Download it instead.')
      return
    }
    setViewLoading(true)
    api
      .fileContent(f.id)
      .then((text) => setViewText(text))
      .catch((e) => setViewError(String(e instanceof Error ? e.message : e)))
      .finally(() => setViewLoading(false))
  }, [])

  // window-level drag events so the dropzone works from anywhere
  useEffect(() => {
    let depth = 0
    const onDragEnter = (e: DragEvent) => {
      if (e.dataTransfer?.types.includes('Files')) {
        depth++
        setDragging(true)
      }
    }
    const onDragLeave = () => {
      depth = Math.max(0, depth - 1)
      if (depth === 0) setDragging(false)
    }
    const onDrop = (e: DragEvent) => {
      e.preventDefault()
      depth = 0
      setDragging(false)
      if (!canWrite) return
      upload(e.dataTransfer?.files ?? null)
    }
    const onDragOver = (e: DragEvent) => e.preventDefault()
    window.addEventListener('dragenter', onDragEnter)
    window.addEventListener('dragleave', onDragLeave)
    window.addEventListener('drop', onDrop)
    window.addEventListener('dragover', onDragOver)
    return () => {
      window.removeEventListener('dragenter', onDragEnter)
      window.removeEventListener('dragleave', onDragLeave)
      window.removeEventListener('drop', onDrop)
      window.removeEventListener('dragover', onDragOver)
    }
  }, [upload, canWrite])

  const empty = files && files.length === 0 && uploads.length === 0

  return (
    <div className="anim-in space-y-6">
      <header className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <h1 className="font-display text-[30px] leading-[1.05] font-semibold tracking-[-0.03em] text-ink md:text-[32px]">Files</h1>
          <p className="mt-1.5 text-sm text-faint">Files that transfer jobs send to your hosts, or bring back from them. Newest first.</p>
        </div>
        <WriteGate>
          <Button variant="primary" onClick={() => inputRef.current?.click()}>
            <FileUp size={16} /> Upload files
          </Button>
        </WriteGate>
      </header>
      <input
        ref={inputRef}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          upload(e.target.files)
          e.target.value = ''
        }}
      />

      {canWrite && !empty && (
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          className={cn(
            'flex w-full items-center justify-center gap-3 rounded-2xl border-2 border-dashed px-5 py-5 text-sm transition-colors cursor-pointer',
            dragging ? 'border-accent bg-accent-soft/50 text-accent-ink' : 'border-line text-faint hover:border-input hover:text-dim',
          )}
        >
          <FileUp size={17} aria-hidden="true" />
          {dragging ? 'Drop to upload' : 'Drop files anywhere on this page, or click to choose'}
        </button>
      )}

      {uploads.length > 0 && (
        <ul aria-label="Uploading" className="space-y-2 rounded-2xl border border-line bg-panel p-4">
          {uploads.map((u) => (
            <li key={u.name}>
              <div className="flex items-center justify-between gap-3 text-sm">
                <span className="truncate font-mono text-[13px] text-ink">{u.name}</span>
                <span className="font-mono text-xs tabular-nums text-faint">{u.pct}%</span>
              </div>
              <div
                className="mt-1.5 h-1.5 rounded-full bg-raised"
                role="progressbar"
                aria-label={`Uploading ${u.name}`}
                aria-valuenow={u.pct}
                aria-valuemin={0}
                aria-valuemax={100}
              >
                <div className="h-1.5 rounded-full bg-cyan transition-all" style={{ width: `${u.pct}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}

      {empty ? (
        <div className={cn('rounded-2xl border bg-panel', dragging ? 'border-accent' : 'border-line')}>
          <EmptyState
            icon={PackageOpen}
            title="No files yet"
            sub="Upload a file here, then point a file-transfer job at it. You can also drop files anywhere on this page."
            action={
              <WriteGate>
                <Button variant="primary" onClick={() => inputRef.current?.click()}>
                  Upload files
                </Button>
              </WriteGate>
            }
          />
        </div>
      ) : (
        files &&
        files.length > 0 && (
          <div className="overflow-x-auto rounded-2xl border border-line bg-panel">
            <table className="w-full min-w-[640px] text-sm">
              <thead>
                <tr className="border-b border-line-soft text-left text-xs text-faint">
                  <th className="px-5 py-3 font-semibold">Name</th>
                  <th className="px-3 py-3 font-semibold">Uploaded</th>
                  <th className="px-3 py-3 text-right font-semibold">Size</th>
                  <th className="px-5 py-3">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {files.map((f) => (
                  <tr key={f.id} className="border-b border-line-soft last:border-b-0">
                    <td className="px-5 py-3">
                      <div className="truncate font-mono text-[13.5px] font-medium text-ink">{f.name}</div>
                      <div className="mt-0.5 font-mono text-xs text-faint" title={`SHA-256 ${f.sha256}`}>
                        SHA-256 {f.sha256.slice(0, 16)}…
                      </div>
                    </td>
                    <td className="px-3 py-3 whitespace-nowrap text-dim">
                      <time dateTime={f.created_at} title={f.created_at}>
                        {fmtDateTime(f.created_at)}
                      </time>
                    </td>
                    <td className="px-3 py-3 text-right font-mono text-[13px] tabular-nums text-dim">{fmtBytes(f.size_bytes)}</td>
                    <td className="px-5 py-3">
                      <div className="flex justify-end gap-2">
                        <Button size="sm" variant="secondary" onClick={() => openView(f)}>
                          View
                        </Button>
                        <a
                          href={api.fileDownloadURL(f.id)}
                          className="inline-flex h-8 items-center rounded-full border border-line bg-panel px-3.5 text-[13px] font-semibold text-ink hover:bg-raised"
                        >
                          Download
                        </a>
                        <WriteGate>
                          <RowMenu label={`Actions for ${f.name}`}>
                            <MenuAction danger onSelect={() =>
                                confirm({
                                  title: `Delete ${f.name}?`,
                                  body: 'The file is removed from the library. Jobs and transfers that use it will fail.',
                                  confirmLabel: 'Delete file',
                                  destructive: true,
                                }).then(
                                  (ok) =>
                                    void (ok &&
                                      api
                                        .deleteFile(f.id)
                                        .then(() => {
                                          toast.success(`${f.name} deleted`)
                                          reload()
                                        })
                                        .catch((e) => toast.error(String(e)))),
                                )
                              }
                            >
                              Delete…
                            </MenuAction>
                          </RowMenu>
                        </WriteGate>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}

      <Drawer
        open={!!viewing}
        onClose={() => setViewing(null)}
        title={viewing?.name ?? 'File'}
        sub={
          viewing
            ? `${fmtDateTime(viewing.created_at)} · ${fmtBytes(viewing.size_bytes)}`
            : undefined
        }
        footer={
          viewing ? (
            <>
              <a
                href={api.fileDownloadURL(viewing.id)}
                className="inline-flex h-8 items-center rounded-full border border-line bg-panel px-3.5 text-[13px] font-semibold text-ink hover:bg-raised"
              >
                Download
              </a>
              <Button size="sm" variant="primary" onClick={() => setViewing(null)}>
                Close
              </Button>
            </>
          ) : undefined
        }
      >
        {viewLoading && <p className="text-sm text-faint">Loading…</p>}
        {viewError && <p className="text-sm text-dim">{viewError}</p>}
        {viewText != null && (
          <pre className="rounded-xl bg-terminal p-4 font-mono text-[12.5px] leading-relaxed break-words whitespace-pre-wrap text-terminal-ink">
            {viewText === '' ? <span className="text-terminal-dim">This file is empty.</span> : viewText}
          </pre>
        )}
      </Drawer>
    </div>
  )
}
