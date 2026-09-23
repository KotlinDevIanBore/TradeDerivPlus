import { Spinner } from '@/components/Spinner'
import { DigitTradeScreen } from '@/features/trading/DigitTradeScreen'
import { TradeScreen } from '@/features/trading/TradeScreen'
import { useMe } from '@/hooks/useQueries'

/**
 * The app's one primary route. Which screen mounts is decided by the runtime
 * flag on /auth/me — not by a build-time constant — so ops can flip back to
 * CFD trading without redeploying the web bundle.
 */
export function MainSurface() {
  const me = useMe()

  if (me.isLoading && !me.data) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (me.data?.digit_contracts_enabled) {
    return <DigitTradeScreen />
  }

  return <TradeScreen />
}
