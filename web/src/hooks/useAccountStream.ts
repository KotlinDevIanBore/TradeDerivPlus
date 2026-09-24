import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { createSocket } from '@/lib/socket'
import { useSessionStore } from '@/store/session'
import { dec } from '@/lib/money'
import { queryKeys } from '@/lib/queryKeys'
import type { BotRun, DigitContract, Trade, Wallet } from '@/types'

export interface AccountEvent {
  type:
    | 'wallet'
    | 'transaction'
    | 'trade_opened'
    | 'trade_closed'
    | 'digit_opened'
    | 'digit_settled'
    | 'bot_run'
    | 'kyc'
    | 'account'
  payload: unknown
}

export interface TransactionEvent {
  transaction_id: string
  status: 'pending' | 'completed' | 'failed'
  type: string
  amount?: string
  reason?: string
  receipt?: string
}

export interface TradeClosedEvent {
  trade: Trade
  reason: 'manual' | 'stop_loss' | 'take_profit'
}

export interface DigitSettledEvent {
  contract: DigitContract
  reason: string
}

/**
 * The user's event channel. One socket for the whole session, not one per
 * account: switching between demo and real no longer tears down and rebuilds
 * a connection, and an event for the account the user is not currently looking
 * at still arrives. Each payload names the account it concerns.
 *
 * This is what makes the deposit screen resolve without polling, tells the UI a
 * position was auto-closed at its stop loss while the user was elsewhere, and
 * unlocks the Withdraw button the moment an admin approves verification.
 */
export function useAccountStream(
  accountId: string | null,
  onEvent?: (event: AccountEvent) => void,
) {
  const token = useSessionStore((s) => s.accessToken)
  const queryClient = useQueryClient()

  const handlerRef = useRef(onEvent)
  handlerRef.current = onEvent

  useEffect(() => {
    if (!token) return

    const socket = createSocket({
      path: '/account',
      token,
      onMessage: (env) => {
        const event = env as AccountEvent
        handlerRef.current?.(event)

        switch (event.type) {
          case 'kyc':
            // An admin approved or rejected verification. Refetch the profile
            // so the Withdraw gate reflects it without waiting out the
            // five-minute stale time.
            queryClient.invalidateQueries({ queryKey: queryKeys.me() })
            break

          case 'account':
            // Freeze or unfreeze: the account list carries the status.
            queryClient.invalidateQueries({ queryKey: queryKeys.accounts() })
            break

          case 'wallet': {
            // The socket pushes real and bonus but not the derived total, so
            // recompute it here rather than writing an undefined into the
            // cache and blanking the hero balance for a frame.
            const w = event.payload as Omit<Wallet, 'total'>
            queryClient.setQueryData<Wallet>(queryKeys.wallet(w.account_id), {
              ...w,
              total: dec(w.real_balance).plus(dec(w.bonus_balance)).toFixed(2),
            })
            break
          }

          case 'transaction': {
            // The payload names its account, which may not be the one on
            // screen, so invalidate by that rather than by the active account.
            const tx = event.payload as { account_id?: string }
            if (tx.account_id) {
              queryClient.invalidateQueries({ queryKey: queryKeys.transactions(tx.account_id) })
            } else if (accountId) {
              queryClient.invalidateQueries({ queryKey: queryKeys.transactions(accountId) })
            }
            break
          }

          case 'trade_opened':
          case 'trade_closed':
            if (accountId) {
              queryClient.invalidateQueries({ queryKey: queryKeys.trades(accountId) })
              queryClient.invalidateQueries({ queryKey: queryKeys.tradeSummary(accountId) })
            }
            break

          case 'bot_run': {
            // Written into the cache rather than invalidated, so the run card
            // moves the moment the server says so. A run emits one of these on
            // start, after every settlement and on stop, which is what makes the
            // card live; useLiveBotRun's 2s poll is only the fallback for a
            // socket that dropped.
            const run = event.payload as BotRun
            if (!run?.id) break
            queryClient.setQueryData(queryKeys.botRun(run.id), run)
            // The live-run list is a page of one, and the shape has to match what
            // the API returns or the card would read an object where it expects a
            // list. A stopped run leaves the list empty, which is how the surface
            // gets its ticket back.
            queryClient.setQueryData(queryKeys.botRuns(run.account_id, 'live'), {
              items: run.status === 'stopped' ? [] : [run],
              total: run.status === 'stopped' ? 0 : 1,
              page: 1,
              limit: 1,
            })
            // And let the history list refetch, so "how did the last one end"
            // is answerable the moment a run stops rather than at the next
            // mount. The prefix key covers every status, including the 'live'
            // entry just written — which refetches to the same value.
            queryClient.invalidateQueries({ queryKey: queryKeys.botRuns(run.account_id) })
            queryClient.invalidateQueries({ queryKey: queryKeys.digitContracts(run.account_id) })
            break
          }

          case 'digit_opened':
          case 'digit_settled': {
            const payload = event.payload as {
              contract?: { account_id?: string }
              account_id?: string
            }
            const id = payload.contract?.account_id ?? payload.account_id ?? accountId
            if (id) {
              queryClient.invalidateQueries({ queryKey: queryKeys.digitContracts(id) })
              queryClient.invalidateQueries({ queryKey: queryKeys.wallet(id) })
              queryClient.invalidateQueries({ queryKey: queryKeys.transactions(id) })
            }
            break
          }
        }
      },
      onStatus: () => {
        // Balance freshness is not what the connection dot reports; that
        // tracks the price feed, which is the one the user watches tick.
      },
    })

    return () => socket.close()
  }, [token, accountId, queryClient])
}
