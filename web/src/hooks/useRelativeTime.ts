import { useEffect, useState } from 'react'

/**
 * Re-renders on an interval so a "3m ago" label ages without a data refetch.
 * Thirty seconds is fine: the label's own resolution is a minute.
 */
export function useRelativeTime(intervalMs = 30_000) {
  const [, setTick] = useState(0)

  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
}
