import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '@/api/client'
import { humanMessage } from '@/components/ErrorState'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Skeleton } from '@/components/ui/Skeleton'
import { createPortal } from 'react-dom'
import { cn } from '@/lib/cn'
import { money, percent } from '@/lib/format'
import { dec } from '@/lib/money'
import type {
  BotLimits,
  DigitContractType,
  DigitQuote,
  DigitSelection,
  TradeSymbol,
  Wallet,
} from '@/types'
import { DigitPicker } from './DigitPicker'

/** 1s is the server's floor; 2s is the default because it is not the floor. */
const INTERVALS = [
  { ms: 1000, label: '1s' },
  { ms: 2000, label: '2s' },
  { ms: 5000, label: '5s' },
]

/**
 * A fixed half hour in v1. It is a real limit rather than an omission — a run
 * that outlives the session that started it is the thing nobody wants — but it
 * is not worth a control until someone asks for one.
 */
const RUN_DURATION_SECONDS = 1800

/**
 * Below this the contract is expensive enough to say so.
 *
 * Was −0.05, chosen when Matches sat alone at −10% and the next worst was −4.5%.
 * Matches is now −4.5% too, so that line would flag nothing at all — the warning
 * would have gone quiet without anyone noticing. The worst band is now −4.5%
 * (Matches, Over 8, Under 1) and the next is −4.0%, so the line moves into that
 * gap and keeps meaning what it meant.
 */
const EXPENSIVE_EXPECTED_RETURN = -0.042

export interface BotTicketSubmit {
  contractType: DigitContractType
  selection: DigitSelection
  stake: string
  intervalMS: number
  limits: BotLimits
}

/** What the parent needs to fetch a quote for what is currently selected. */
export interface BotTicketConfig {
  contractType: DigitContractType
  selection: DigitSelection
  stake: string
}

/**
 * The automated ticket: what a run will do, before it does it.
 *
 * It takes its quote as a prop rather than fetching one, so it can be rendered
 * and asserted on without a query client — the same arrangement OrderTicket uses,
 * and the reason the manual DigitTicket has no component test.
 *
 * The copy here is deliberate and worth leaving alone. Every contract on this
 * platform has negative expected value, so the ticket states the cost per
 * contract and the worst case in money, and never says or implies that a run
 * makes money. What it offers is a stake you set once and limits you commit to
 * before the first contract rather than during the fifth.
 */
export function BotTicket({
  contractType,
  symbol,
  wallet,
  quote,
  maxStake,
  disabled,
  disabledReason,
  submitting,
  error,
  isReal = false,
  realMoneyEnabled = false,
  onConfigChange,
  onSubmit,
  flushTop = false,
  actionsTarget = null,
}: {
  contractType: DigitContractType
  symbol: TradeSymbol | null
  wallet?: Wallet
  /** The quote for the current selection and stake, or undefined while it loads. */
  quote?: DigitQuote
  /** The server's per-contract ceiling, shown as a hint so it is not a surprise. */
  maxStake?: string
  disabled: boolean
  disabledReason?: string
  submitting: boolean
  error: ApiError | null
  /** A real account: runs are refused on one until real money is switched on. */
  isReal?: boolean
  realMoneyEnabled?: boolean
  onConfigChange?: (config: BotTicketConfig) => void
  onSubmit: (values: BotTicketSubmit) => void
  flushTop?: boolean
  /** See DigitTicket: the pinned action bar on a phone, null on a desktop. */
  actionsTarget?: HTMLElement | null
}) {
  const [side, setSide] = useState<'primary' | 'secondary'>('primary')
  const [digit, setDigit] = useState(0)
  const [barrier, setBarrier] = useState(4)
  const [stake, setStake] = useState('10')
  const [intervalMS, setIntervalMS] = useState(2000)
  const [stopLoss, setStopLoss] = useState('50')
  const [takeProfit, setTakeProfit] = useState('')
  const [maxLosses, setMaxLosses] = useState('5')
  const [maxContracts, setMaxContracts] = useState('50')

  const available = dec(wallet?.real_balance).plus(dec(wallet?.bonus_balance))
  const stakeValue = dec(stake)
  const stopLossValue = dec(stopLoss)

  const selection = useMemo(
    () => selectionFor(contractType, side, digit, barrier),
    [contractType, side, digit, barrier],
  )

  // The parent fetches the quote, so it has to know what is selected. Reported
  // rather than lifted, so the ticket keeps its own state and stays testable.
  useEffect(() => {
    onConfigChange?.({ contractType, selection, stake })
  }, [contractType, selection, stake, onConfigChange])

  const demoOnly = isReal && !realMoneyEnabled

  // A run places repeatedly, so a stake that is a large share of the balance is a
  // different risk from the same stake placed once by hand. The server enforces
  // this; saying it here means a user meets it before pressing Start.
  const stakeCeiling = available.mul(dec('0.1')).toDecimalPlaces(2)

  const stakeError =
    stake !== '' && stakeValue.lessThanOrEqualTo(0)
      ? 'Enter a stake greater than zero'
      : maxStake && stakeValue.greaterThan(dec(maxStake))
        ? `The most you can stake on one contract is ${money(maxStake)}`
        : stakeValue.greaterThan(stakeCeiling)
          ? `A run can stake at most a tenth of your balance, which is ${money(stakeCeiling.toString())}`
          : error?.field === 'stake'
            ? error.message
            : undefined

  const stopLossError =
    stopLoss === '' || stopLossValue.lessThanOrEqualTo(0)
      ? 'Set a stop loss so the run knows when to stop'
      : stopLossValue.greaterThan(available)
        ? 'The stop loss cannot be more than your balance'
        : error?.field === 'stop_loss'
          ? error.message
          : undefined

  const takeProfitError =
    takeProfit !== '' && dec(takeProfit).lessThanOrEqualTo(0)
      ? 'Enter a take profit greater than zero, or leave it empty'
      : error?.field === 'take_profit'
        ? error.message
        : undefined

  const expected = quote ? dec(quote.expected_return) : null
  const expensive = expected !== null && expected.lessThan(dec(String(EXPENSIVE_EXPECTED_RETURN)))

  const canSubmit =
    !disabled &&
    !submitting &&
    !demoOnly &&
    !stakeError &&
    !stopLossError &&
    !takeProfitError &&
    stakeValue.greaterThan(0) &&
    Boolean(symbol) &&
    Boolean(quote)

  const start = () => {
    if (!canSubmit) return
    const limits: BotLimits = {
      stop_loss: stopLoss,
      max_duration_s: RUN_DURATION_SECONDS,
    }
    if (takeProfit !== '') limits.take_profit = takeProfit
    if (maxLosses !== '') limits.max_consecutive_losses = Number(maxLosses)
    if (maxContracts !== '') limits.max_contracts = Number(maxContracts)

    onSubmit({ contractType, selection, stake, intervalMS, limits })
  }

  const startButton = (
    <Button block size="lg" disabled={!canSubmit} loading={submitting} onClick={start}>
      Start auto run
    </Button>
  )

  return (
    <div
      className={cn(
        'bg-surface p-4 lg:rounded-card lg:border lg:border-border',
        flushTop ? 'pt-2' : 'rounded-t-3xl border-t border-border',
        // No height cap and no inner scroll. Both existed because this used to
        // be a sticky sheet over the chart, tall enough at 524px to cover the
        // tabs and the digit row. It sits in the page's flow now, so the page
        // scrolls it; a scroll region inside a scrolling page is a trap on a
        // phone. Only the Start button is pinned, via actionsTarget.
      )}
    >
      <SideChoice contractType={contractType} value={side} onChange={setSide} />

      {contractType === 'matches_differs' ? (
        <div className="mt-3">
          <DigitPicker label="Digit" value={digit} onChange={setDigit} />
        </div>
      ) : null}
      {contractType === 'over_under' ? (
        <div className="mt-3">
          <DigitPicker label="Barrier" value={barrier} onChange={setBarrier} />
        </div>
      ) : null}

      <div className="mt-3">
        <Input
          id="bot-stake"
          label="Stake per contract"
          inputMode="decimal"
          prefix="$"
          value={stake}
          onChange={(e) => setStake(e.target.value)}
          className="tnum"
          error={stakeError}
          hint={maxStake ? `Up to ${money(maxStake)} a contract` : undefined}
        />
      </div>

      <div className="mt-3">
        <p className="text-meta font-medium text-muted">Place a contract every</p>
        <div className="mt-1.5 flex gap-1.5" role="group" aria-label="Place a contract every">
          {INTERVALS.map(({ ms, label }) => (
            <button
              key={ms}
              type="button"
              aria-pressed={intervalMS === ms}
              onClick={() => setIntervalMS(ms)}
              className={cn(
                'rounded-lg border px-3 py-1 text-meta font-medium transition-colors',
                intervalMS === ms
                  ? 'border-accent bg-accent-subtle text-accent'
                  : 'border-border text-muted hover:bg-elevated hover:text-text',
              )}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      <div className="mt-4 border-t border-border pt-3">
        <p className="text-meta font-semibold text-text">Stop the run when</p>

        <div className="mt-2 grid grid-cols-2 gap-2">
          <Input
            id="bot-stop-loss"
            label="I have lost"
            inputMode="decimal"
            prefix="$"
            value={stopLoss}
            onChange={(e) => setStopLoss(e.target.value)}
            className="tnum"
            error={stopLossError}
          />
          <Input
            id="bot-take-profit"
            label="I am up"
            inputMode="decimal"
            prefix="$"
            placeholder="Optional"
            value={takeProfit}
            onChange={(e) => setTakeProfit(e.target.value)}
            className="tnum"
            error={takeProfitError}
          />
          <Input
            id="bot-max-losses"
            label="Losses in a row"
            inputMode="numeric"
            value={maxLosses}
            onChange={(e) => setMaxLosses(e.target.value)}
            className="tnum"
          />
          <Input
            id="bot-max-contracts"
            label="Contracts placed"
            inputMode="numeric"
            value={maxContracts}
            onChange={(e) => setMaxContracts(e.target.value)}
            className="tnum"
          />
        </div>
        <p className="mt-2 text-nano text-faint">Runs for up to 30 minutes.</p>
      </div>

      <div className="mt-4 rounded-lg bg-elevated p-3">
        {quote ? (
          <>
            <p className="text-meta text-muted">
              Pays{' '}
              <span className="font-semibold text-text tnum">{money(quote.potential_payout)}</span>{' '}
              on a win ·{' '}
              <span className="font-semibold text-text tnum">
                {percent(dec(quote.win_probability).mul(100).toString(), 0)}
              </span>{' '}
              chance
            </p>
            <p
              className={cn(
                'mt-1 text-meta font-semibold tnum',
                expensive ? 'text-warning' : 'text-muted',
              )}
            >
              Expected return {percent(dec(quote.expected_return).mul(100).toString(), 2)} per
              contract
            </p>
            {expensive ? (
              <p className="mt-1 text-nano text-warning">This contract costs the most per play.</p>
            ) : null}
          </>
        ) : (
          <Skeleton className="h-[42px] w-full" />
        )}

        {/* The figure a user actually needs: the floor is not the worst case,
            because the contract in flight when it trips still has to settle. */}
        <p className="mt-2 border-t border-border pt-2 text-meta text-muted">
          Worst case you lose{' '}
          <span className="font-semibold text-text tnum">
            {money(stopLossValue.plus(stakeValue.isNegative() ? 0 : stakeValue).toString())}
          </span>{' '}
          <span className="text-faint">(stop loss plus the contract in flight)</span>
        </p>
      </div>

      {demoOnly ? (
        <p className="mt-3 text-meta text-warning">
          Automated runs are available on demo accounts for now. Switch to your demo account to
          start one.
        </p>
      ) : null}
      {error && !error.field ? (
        <p className="mt-2 text-meta text-accent">{humanMessage(error)}</p>
      ) : null}
      {disabledReason ? <p className="mt-2 text-meta text-warning">{disabledReason}</p> : null}

      {actionsTarget ? createPortal(startButton, actionsTarget) : <div className="mt-4">{startButton}</div>}

      <p className="mt-2 text-center text-nano text-faint">
        Places your settings for you until a stop condition is met.
      </p>
    </div>
  )
}

/**
 * Which of the two outcomes the run will back. The manual ticket offers both as
 * buttons because a person picks one per contract; a run picks once, so this is a
 * choice rather than an action.
 */
function SideChoice({
  contractType,
  value,
  onChange,
}: {
  contractType: DigitContractType
  value: 'primary' | 'secondary'
  onChange: (side: 'primary' | 'secondary') => void
}) {
  const [primary, secondary] = sideLabels(contractType)
  return (
    <div>
      <p className="text-meta font-medium text-muted">Back</p>
      <div className="mt-1.5 grid grid-cols-2 gap-2" role="group" aria-label="Back">
        {(
          [
            ['primary', primary],
            ['secondary', secondary],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            aria-pressed={value === key}
            onClick={() => onChange(key)}
            className={cn(
              'rounded-lg border px-3 py-2 text-body font-semibold transition-colors',
              value === key
                ? 'border-accent bg-accent-subtle text-accent'
                : 'border-border text-muted hover:bg-elevated hover:text-text',
            )}
          >
            {label}
          </button>
        ))}
      </div>
    </div>
  )
}

function sideLabels(type: DigitContractType): [string, string] {
  switch (type) {
    case 'even_odd':
      return ['Even', 'Odd']
    case 'matches_differs':
      return ['Matches', 'Differs']
    case 'over_under':
      return ['Over', 'Under']
  }
}

function selectionFor(
  type: DigitContractType,
  side: 'primary' | 'secondary',
  digit: number,
  barrier: number,
): DigitSelection {
  switch (type) {
    case 'even_odd':
      return { parity: side === 'primary' ? 'even' : 'odd' }
    case 'matches_differs':
      return { mode: side === 'primary' ? 'match' : 'differ', digit }
    case 'over_under':
      return { side: side === 'primary' ? 'over' : 'under', barrier }
  }
}
