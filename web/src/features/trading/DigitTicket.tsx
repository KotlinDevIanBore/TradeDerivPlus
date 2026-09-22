import { useMemo, useState } from 'react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { humanMessage } from '@/components/ErrorState'
import { cn } from '@/lib/cn'
import { payoutPercentLabel, payoutRatioFor, winCredit } from '@/lib/digitPayout'
import { money } from '@/lib/format'
import { dec } from '@/lib/money'
import type { DigitContractType, DigitSelection, TradeSymbol, Wallet } from '@/types'

const QUICK_STAKES = [5, 10, 50]

export interface DigitTicketSubmit {
  contractType: DigitContractType
  selection: DigitSelection
  stake: string
  /** Which of the two outcome buttons was pressed. */
  side: 'primary' | 'secondary'
}

/**
 * Digit order ticket. Stake UX mirrors the CFD ticket; selection and payout
 * buttons are new. Payout figures come from the client mirror of the server's
 * static table (see digitPayout.ts) until a quote API exists.
 */
export function DigitTicket({
  contractType,
  symbol,
  wallet,
  disabled,
  disabledReason,
  submitting,
  error,
  onSubmit,
}: {
  contractType: DigitContractType
  symbol: TradeSymbol | null
  wallet?: Wallet
  disabled: boolean
  disabledReason?: string
  submitting: boolean
  error: ApiError | null
  onSubmit: (values: DigitTicketSubmit) => void
}) {
  const [stake, setStake] = useState('10')
  const [digit, setDigit] = useState(0)
  const [barrier, setBarrier] = useState(4)

  const available = dec(wallet?.real_balance).plus(dec(wallet?.bonus_balance))
  const stakeValue = dec(stake)

  const stakeError =
    stake !== '' && stakeValue.lessThanOrEqualTo(0)
      ? 'Enter a stake greater than zero'
      : stakeValue.greaterThan(available)
        ? `You have ${money(available.toString())} available`
        : error?.field === 'stake'
          ? error.message
          : undefined

  const primary = useMemo(() => selectionFor(contractType, 'primary', digit, barrier), [contractType, digit, barrier])
  const secondary = useMemo(
    () => selectionFor(contractType, 'secondary', digit, barrier),
    [contractType, digit, barrier],
  )

  const primaryRatio = payoutRatioFor(contractType, primary)
  const secondaryRatio = payoutRatioFor(contractType, secondary)
  const primaryCredit = primaryRatio ? winCredit(stakeValue, primaryRatio) : null
  const secondaryCredit = secondaryRatio ? winCredit(stakeValue, secondaryRatio) : null

  const canSubmit =
    !disabled &&
    !submitting &&
    !stakeError &&
    stakeValue.greaterThan(0) &&
    Boolean(symbol) &&
    primaryRatio !== null &&
    secondaryRatio !== null

  const fire = (side: 'primary' | 'secondary') => {
    if (!canSubmit) return
    onSubmit({
      contractType,
      selection: side === 'primary' ? primary : secondary,
      stake,
      side,
    })
  }

  return (
    <div className="rounded-t-3xl border-t border-border bg-surface p-4 lg:rounded-card lg:border">
      {contractType === 'matches_differs' ? (
        <DigitPicker label="Digit" value={digit} onChange={setDigit} />
      ) : null}
      {contractType === 'over_under' ? (
        <DigitPicker label="Barrier" value={barrier} onChange={setBarrier} exclude={[]} />
      ) : null}

      <div className="mt-3">
        <label htmlFor="digit-stake" className="text-meta font-medium text-muted">
          Stake
        </label>
        <div className="mt-1.5 flex items-center gap-2">
          <span className="text-body text-muted">$</span>
          <Input
            id="digit-stake"
            inputMode="decimal"
            value={stake}
            onChange={(e) => setStake(e.target.value)}
            className="tnum"
            aria-invalid={Boolean(stakeError)}
          />
        </div>
        {stakeError ? <p className="mt-1 text-meta text-accent">{stakeError}</p> : null}
        <div className="mt-2 flex flex-wrap gap-1.5">
          {QUICK_STAKES.map((n) => (
            <button
              key={n}
              type="button"
              onClick={() => setStake(String(dec(stake).plus(n)))}
              className="rounded-lg border border-border px-2.5 py-1 text-meta font-medium text-muted hover:bg-elevated hover:text-text"
            >
              +{n}
            </button>
          ))}
          <button
            type="button"
            onClick={() => setStake(available.toFixed(2))}
            className="rounded-lg border border-border px-2.5 py-1 text-meta font-medium text-muted hover:bg-elevated hover:text-text"
          >
            Max
          </button>
        </div>
      </div>

      <p className="mt-3 text-meta text-faint">
        Available {money(available.toString())}
        {wallet ? (dec(wallet.bonus_balance).greaterThan(0) ? ' · practice funds first' : '') : ''}
      </p>

      {error && !error.field ? (
        <p className="mt-2 text-meta text-accent">{humanMessage(error)}</p>
      ) : null}
      {disabledReason ? <p className="mt-2 text-meta text-warning">{disabledReason}</p> : null}

      <div className="mt-4 grid grid-cols-2 gap-2">
        <OutcomeButton
          label={primaryLabel(contractType)}
          credit={primaryCredit}
          ratio={primaryRatio}
          variant="primary"
          disabled={!canSubmit}
          loading={submitting}
          onClick={() => fire('primary')}
        />
        <OutcomeButton
          label={secondaryLabel(contractType)}
          credit={secondaryCredit}
          ratio={secondaryRatio}
          variant="secondary"
          disabled={!canSubmit}
          loading={submitting}
          onClick={() => fire('secondary')}
        />
      </div>
    </div>
  )
}

function DigitPicker({
  label,
  value,
  onChange,
}: {
  label: string
  value: number
  onChange: (n: number) => void
  exclude?: number[]
}) {
  return (
    <div>
      <p className="text-meta font-medium text-muted">{label}</p>
      <div className="mt-1.5 flex flex-wrap gap-1">
        {Array.from({ length: 10 }, (_, d) => (
          <button
            key={d}
            type="button"
            onClick={() => onChange(d)}
            aria-pressed={value === d}
            className={cn(
              'flex h-9 w-9 items-center justify-center rounded-lg text-body font-semibold transition-colors',
              value === d
                ? 'bg-accent text-white'
                : 'bg-elevated text-muted hover:text-text',
            )}
          >
            {d}
          </button>
        ))}
      </div>
    </div>
  )
}

function OutcomeButton({
  label,
  credit,
  ratio,
  variant,
  disabled,
  loading,
  onClick,
}: {
  label: string
  credit: ReturnType<typeof dec> | null
  ratio: ReturnType<typeof dec> | null
  variant: 'primary' | 'secondary'
  disabled: boolean
  loading: boolean
  onClick: () => void
}) {
  return (
    <Button
      size="lg"
      block
      disabled={disabled}
      loading={loading}
      onClick={onClick}
      className={cn(
        variant === 'secondary' &&
          'border border-accent/40 bg-transparent text-accent hover:bg-accent-subtle',
      )}
      variant={variant === 'primary' ? 'primary' : 'outline'}
    >
      <span className="flex flex-col items-center leading-tight">
        <span>{label}</span>
        {credit && ratio ? (
          <span className="text-meta font-medium opacity-90 tnum">
            {money(credit.toFixed(2))} · {payoutPercentLabel(ratio)}
          </span>
        ) : null}
      </span>
    </Button>
  )
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

function primaryLabel(type: DigitContractType): string {
  switch (type) {
    case 'even_odd':
      return 'Even'
    case 'matches_differs':
      return 'Match'
    case 'over_under':
      return 'Over'
  }
}

function secondaryLabel(type: DigitContractType): string {
  switch (type) {
    case 'even_odd':
      return 'Odd'
    case 'matches_differs':
      return 'Differ'
    case 'over_under':
      return 'Under'
  }
}
