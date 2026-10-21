import { useEffect, useState } from 'react'

// useNow re-renders on an interval so clocks and "in 4 min" labels keep moving.
export function useNow(everyMs = 30_000): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), everyMs)
    return () => clearInterval(t)
  }, [everyMs])
  return now
}
