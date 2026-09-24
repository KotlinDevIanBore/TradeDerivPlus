import { api, qs } from './client'
import type { BotLimits, BotRun, DigitContractType, DigitSelection, Page } from '@/types'

/**
 * Automated runs. Starting and stopping are on the server's tight placement
 * bucket, the same one that guards opening a contract; reading runs is not, so a
 * client watching a live run is never throttled into blindness.
 */

export interface StartRunInput {
  account_id: string
  symbol: string
  contract_type: DigitContractType
  selection: DigitSelection
  /** Money as a string, never a number, so nothing is lost to float parsing. */
  stake: string
  interval_ms: number
  limits: BotLimits
}

export const botApi = {
  start: (input: StartRunInput) => api.post<BotRun>('/bot/runs', input),

  // Idempotent server-side: stopping an already-stopped run returns it rather
  // than erroring, so a second tap is harmless.
  stop: (runID: string) => api.post<BotRun>(`/bot/runs/${runID}/stop`),

  get: (runID: string) => api.get<BotRun>(`/bot/runs/${runID}`),

  list: (
    accountId: string,
    status?: 'live' | 'stopped',
    opts: { limit?: number; page?: number } = {},
  ) =>
    api.get<Page<BotRun>>(
      `/bot/runs${qs({
        account_id: accountId,
        status: status || undefined,
        limit: opts.limit ?? 25,
        page: opts.page ?? 1,
      })}`,
    ),
}
