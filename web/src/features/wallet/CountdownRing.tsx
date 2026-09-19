import { useEffect, useState } from 'react'
import { Smartphone } from 'lucide-react'

/**
 * The waiting indicator on the deposit pending step. It counts a fixed window
 * down rather than spinning indefinitely, so the wait has a visible end.
 */
export function CountdownRing({
  seconds,
  onElapsed,
}: {
  seconds: number
  onElapsed?: () => void
}) {
  const [remaining, setRemaining] = useState(seconds)

  useEffect(() => {
    setRemaining(seconds)
    const id = setInterval(() => {
      setRemaining((r) => {
        if (r <= 1) {
          clearInterval(id)
          onElapsed?.()
          return 0
        }
        return r - 1
      })
    }, 1000)
    return () => clearInterval(id)
    // onElapsed is intentionally not a dependency: it should fire once per window.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seconds])

  const radius = 54
  const circumference = 2 * Math.PI * radius
  const progress = seconds > 0 ? remaining / seconds : 0

  return (
    <div className="relative mx-auto h-32 w-32">
      <svg viewBox="0 0 120 120" className="h-full w-full -rotate-90">
        <circle cx="60" cy="60" r={radius} fill="none" stroke="#232326" strokeWidth="4" />
        <circle
          cx="60"
          cy="60"
          r={radius}
          fill="none"
          stroke="#E24B4A"
          strokeWidth="4"
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={circumference * (1 - progress)}
          className="transition-[stroke-dashoffset] duration-1000 ease-linear"
        />
      </svg>

      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <Smartphone className="h-7 w-7 text-muted" aria-hidden />
        <span className="mt-1 text-body font-semibold tnum" aria-live="off">
          {remaining}s
        </span>
      </div>
    </div>
  )
}
