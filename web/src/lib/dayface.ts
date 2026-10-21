// Wall-clock arithmetic for the 24-hour dial. A civil day is not always 24
// hours long (daylight saving), so nothing here counts milliseconds: a day
// runs from one local midnight to the next, and a moment sits on the face at
// the hour and minute a clock on the wall would show.

export function startOfDay(d: Date): Date {
  const s = new Date(d)
  s.setHours(0, 0, 0, 0)
  return s
}

// The next local midnight, 23, 24 or 25 hours after startOfDay.
export function endOfDay(d: Date): Date {
  const e = startOfDay(d)
  e.setDate(e.getDate() + 1)
  return e
}

// degOnFace: degrees clockwise from midnight at the top, by the wall clock of
// timeZone, or of the browser when none is given.
export function degOnFace(d: Date, timeZone?: string): number {
  let hour = d.getHours()
  let minute = d.getMinutes()
  if (timeZone) {
    try {
      const parts = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone }).formatToParts(d)
      const num = (type: string) => Number(parts.find((p) => p.type === type)?.value ?? 0)
      hour = num('hour')
      minute = num('minute')
    } catch {
      // not a zone the browser knows: keep the browser's clock
    }
  }
  return ((hour * 60 + minute) / 1440) * 360
}
