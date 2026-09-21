import { Suspense, lazy } from 'react'
import { ChartSkeleton } from './ChartSkeleton'

/**
 * The charting library is the largest dependency in the app. Loading it only
 * when the Trade screen renders keeps Home, Wallet and Portfolio light, which
 * matters on a mobile connection.
 */
const PriceChart = lazy(() =>
  import('./PriceChart').then((m) => ({ default: m.PriceChart })),
)

export function PriceChartLazy(props: React.ComponentProps<typeof PriceChart>) {
  return (
    <Suspense fallback={<ChartSkeleton />}>
      <PriceChart {...props} />
    </Suspense>
  )
}
