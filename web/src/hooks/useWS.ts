import { useEffect, useRef } from 'react'
import type { WSMessage } from '@/lib/types'

/**
 * Subscribes to server topics over WebSocket with auto-reconnect.
 * topics: ['activity', 'run:<id>'] — callers pass a stable array or a joined key.
 */
export function useWS(topicsKey: string, onMessage: (msg: WSMessage) => void) {
  const cb = useRef(onMessage)
  cb.current = onMessage

  useEffect(() => {
    const topics = topicsKey.split(',').filter(Boolean)
    if (topics.length === 0) return

    let ws: WebSocket | null = null
    let closed = false
    let retry: ReturnType<typeof setTimeout> | undefined

    const connect = () => {
      if (closed) return
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${location.host}/api/v1/ws?topics=${encodeURIComponent(topics.join(','))}`)
      ws.onmessage = (e) => {
        try {
          cb.current(JSON.parse(e.data) as WSMessage)
        } catch { /* ignore malformed */ }
      }
      ws.onclose = () => {
        if (!closed) retry = setTimeout(connect, 1500)
      }
      ws.onerror = () => ws?.close()
    }
    connect()

    return () => {
      closed = true
      if (retry) clearTimeout(retry)
      ws?.close()
    }
  }, [topicsKey])
}