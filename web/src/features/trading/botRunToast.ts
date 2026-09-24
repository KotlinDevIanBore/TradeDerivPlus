import { toast } from '@/components/ui/Toast'
import { signedMoney } from '@/lib/format'
import type { BotRun, BotStopReason } from '@/types'

/**
 * What to say when a run ends, and in what tone.
 *
 * Grouped by where the ending came from, because the three groups mean different
 * things to the person who started it. A limit they set is their own instruction
 * being honoured — worth confirming, not apologising for. Something the platform
 * imposed needs explaining, because they did not ask for it. A fault needs owning.
 *
 * Only `take_profit` is success: it is the one ending that means the run did what
 * the user hoped. `stop_loss` is the error tone deliberately — the limit worked,
 * but money was lost, and a green tick over a loss would read as a taunt.
 */
const ENDINGS: Record<
  BotStopReason,
  { tone: 'success' | 'info' | 'error'; title: string; body?: string }
> = {
  // Asked for.
  take_profit: { tone: 'success', title: 'Auto run hit its take profit' },
  user: { tone: 'info', title: 'Auto run stopped' },
  max_contracts: { tone: 'info', title: 'Auto run finished its contracts' },
  max_duration: { tone: 'info', title: 'Auto run reached its time limit' },
  max_consecutive_losses: {
    tone: 'error',
    title: 'Auto run stopped after too many losses in a row',
  },
  stop_loss: { tone: 'error', title: 'Auto run hit its stop loss' },

  // Imposed.
  insufficient_funds: {
    tone: 'error',
    title: 'Auto run stopped',
    body: 'Not enough balance to keep going',
  },
  account_frozen: { tone: 'error', title: 'Auto run stopped', body: 'This account is frozen' },
  market_closed: { tone: 'error', title: 'Auto run stopped', body: 'This market is not available' },
  admin_kill: {
    tone: 'error',
    title: 'Auto run stopped',
    body: 'Automated trading was turned off',
  },
  // One run stopped from the console, which is not the same news: automated
  // trading is still on and they can start another.
  admin_stop: { tone: 'error', title: 'Auto run stopped', body: 'Stopped by support' },
  disabled: { tone: 'error', title: 'Auto run stopped', body: 'Automated trading was turned off' },

  // Faults.
  feed_down: {
    tone: 'error',
    title: 'Auto run stopped',
    body: 'The price feed stopped delivering',
  },
  restart: {
    tone: 'info',
    title: 'Auto run stopped',
    body: 'The service restarted. Start again when you are ready',
  },
  shutdown: {
    tone: 'info',
    title: 'Auto run stopped',
    body: 'The service restarted. Start again when you are ready',
  },
  error: { tone: 'error', title: 'Auto run stopped', body: 'Something went wrong on our side' },
}

/** The realised figure, which every ending should carry: it is what happened. */
function outcome(run: BotRun): string {
  return `${signedMoney(run.realised_pnl)} over ${run.contracts_placed} contract${
    run.contracts_placed === 1 ? '' : 's'
  }`
}

/**
 * The last run announced, so an ending is only ever announced once.
 *
 * A stop is broadcast by whoever wrote it, and on a user stop that can be two
 * parties: the request that finalised the row, and the run's own goroutine
 * noticing it should exit. Both send the same stopped row, and two identical
 * toasts for one stop reads as a bug even though the run stopped exactly once.
 *
 * One id is enough rather than a set of them: an account can only have one live
 * run, so endings cannot interleave, and duplicates of the same ending arrive
 * together.
 */
let lastAnnounced: string | null = null

export function announceRunStopped(run: BotRun) {
  if (run.id === lastAnnounced) return
  lastAnnounced = run.id

  const ending = run.stop_reason ? ENDINGS[run.stop_reason] : undefined
  if (!ending) {
    toast.info('Auto run stopped', outcome(run))
    return
  }
  const body = ending.body ? `${ending.body} · ${outcome(run)}` : outcome(run)
  toast[ending.tone](ending.title, body)
}
