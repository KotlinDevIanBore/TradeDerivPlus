import { useEffect, useId, useMemo, useState } from 'react'
import { AlertTriangle, Plus } from 'lucide-react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { humanMessage } from '@/components/ErrorState'
import { cn } from '@/lib/cn'
import { money, price as fmtPrice, signedMoney } from '@/lib/format'
import { dec } from '@/lib/money'
import { pnl, validateStopLoss, validateTakeProfit } from '@/lib/pnl'
import type { Direction, TradeSymbol, Wallet } from '@/types'

const QUICK_STAKES = [5, 10, 50]

export interface TicketValues {
  direction: Direction
  stake: string
  stopLoss: string | null
  takeProfit: string | null
}

/**
 * The order ticket. It owns no data of its own: the container passes the live
 * price, the balance and the submit callback, and receives the current values
 * back so the chart can draw the stop loss and take profit lines.
 */
export function OrderTicket({
  symbol,
  currentPrice,
  wallet,
  disabled,
  disabledReason,
  submitting,
  error,
  onSubmit,
  onChange,
  initial,
  expiredSignal,
  isDemo,
}: {
  symbol: TradeSymbol | null
  currentPrice: string | undefined
  wallet?: Wallet
  disabled: boolean
  disabledReason?: string
  submitting: boolean
  error: ApiError | null
  onSubmit: (values: TicketValues) => void
  onChange?: (values: TicketValues) => void
  initial?: Partial<TicketValues>
  /** Set when arriving from a signal that has aged out. */
  expiredSignal?: boolean
  isDemo: boolean
}) {
  const [direction, setDirection] = useState<Direction>(initial?.direction ?? 'buy')
  const [stake, setStake] = useState(initial?.stake ?? '10')
  const [slEnabled, setSlEnabled] = useState(Boolean(initial?.stopLoss))
  const [tpEnabled, setTpEnabled] = useState(Boolean(initial?.takeProfit))
  const [stopLoss, setStopLoss] = useState(initial?.stopLoss ?? '')
  const [takeProfit, setTakeProfit] = useState(initial?.takeProfit ?? '')

  // Re-apply when the container hands over a new prefill, e.g. from a signal.
  useEffect(() => {
    if (!initial) return
    if (initial.direction) setDirection(initial.direction)
    if (initial.stopLoss) {
      setStopLoss(initial.stopLoss)
      setSlEnabled(true)
    }
    if (initial.takeProfit) {
      setTakeProfit(initial.takeProfit)
      setTpEnabled(true)
    }
  }, [initial])

  const available = dec(wallet?.real_balance).plus(dec(wallet?.bonus_balance))
  const stakeValue = dec(stake)
  const entry = currentPrice ? dec(currentPrice) : null

  const values: TicketValues = useMemo(
    () => ({
      direction,
      stake,
      stopLoss: slEnabled && stopLoss ? stopLoss : null,
      takeProfit: tpEnabled && takeProfit ? takeProfit : null,
    }),
    [direction, stake, slEnabled, stopLoss, tpEnabled, takeProfit],
  )

  useEffect(() => onChange?.(values), [values, onChange])

  // One message at a time, under the offending field.
  const stakeError =
    stake !== '' && stakeValue.lessThanOrEqualTo(0)
      ? 'Enter a stake greater than zero'
      : stakeValue.greaterThan(available)
        ? `You have ${money(available.toString())} available`
        : error?.field === 'stake'
          ? error.message
          : undefined

  const slError =
    slEnabled && stopLoss && entry
      ? (validateStopLoss(direction, entry, dec(stopLoss)) ?? undefined)
      : error?.field === 'stop_loss'
        ? error.message
        : undefined

  const tpError =
    tpEnabled && takeProfit && entry
      ? (validateTakeProfit(direction, entry, dec(takeProfit)) ?? undefined)
      : error?.field === 'take_profit'
        ? error.message
        : undefined

  /**
   * How far a level sits from the live price, and what reaching it is worth at
   * the current stake. The money figure comes from the same pnl() the server
   * mirrors on close, so the preview cannot drift from the settled result.
   */
  const levelHint = (level: string): string | undefined => {
    if (!entry || !level || entry.isZero()) return undefined

    const pct = dec(level).minus(entry).dividedBy(entry).times(100)
    // "away" rather than "from price", so the projection below fits one line in
    // the ticket's two-column grid.
    const distance = `${pct.isNegative() ? '' : '+'}${pct.toFixed(2)}% away`
    if (stakeValue.lessThanOrEqualTo(0)) return `${distance} from price`

    const projected = pnl(direction, entry, dec(level), stakeValue)
    return `${distance} · ${signedMoney(projected.toString())} if hit`
  }

  /**
   * What a 1% move in the user's favour pays at this stake. This product has no
   * fixed payout -- profit scales with the move -- so this is the honest
   * equivalent, and it is the fastest way to see that a $10 stake is $0.10 a
   * percent. Derived through pnl() rather than divided by a hundred, so it
   * follows the model if the model ever changes.
   */
  const perPercentMove = useMemo(() => {
    if (!entry || entry.isZero() || stakeValue.lessThanOrEqualTo(0)) return null
    const favourable = direction === 'sell' ? entry.times(0.99) : entry.times(1.01)
    return pnl(direction, entry, favourable, stakeValue)
  }, [entry, direction, stakeValue])

  const canSubmit =
    !disabled && !stakeError && !slError && !tpError && stake !== '' && stakeValue.greaterThan(0)

  return (
    <div className="rounded-t-3xl border-t border-border bg-surface p-4 lg:rounded-card lg:border">
      {expiredSignal ? (
        <div className="mb-3 flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/[0.06] px-3 py-2">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" aria-hidden />
          <p className="text-meta text-muted">
            That signal has aged. Its levels are filled in, but you'll open at the current price.
          </p>
        </div>
      ) : null}

      {/* The one place green and red sit side by side. */}
      <div className="grid grid-cols-2 gap-2" role="group" aria-label="Direction">
        {(['buy', 'sell'] as Direction[]).map((d) => (
          <button
            key={d}
            type="button"
            onClick={() => setDirection(d)}
            aria-pressed={direction === d}
            className={cn(
              'h-12 rounded-xl text-card font-bold uppercase tracking-wide transition-all',
              d === 'buy' ? 'bg-profit text-black' : 'bg-accent text-white',
              direction !== d && 'opacity-30',
            )}
          >
            {d}
          </button>
        ))}
      </div>

      <div className="mt-3">
        <Input
          label="Stake"
          type="text"
          inputMode="decimal"
          prefix="$"
          value={stake}
          onChange={(e) => setStake(e.target.value.replace(/[^\d.]/g, ''))}
          error={stakeError}
        />
        <div className="mt-2 flex gap-2">
          {QUICK_STAKES.map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => setStake(dec(stake).plus(v).toString())}
              className="flex-1 rounded-lg border border-border py-1.5 text-meta text-muted transition-colors hover:border-accent hover:text-text"
            >
              +{v}
            </button>
          ))}
          <button
            type="button"
            onClick={() => setStake(available.toFixed(2))}
            className="flex-1 rounded-lg border border-border py-1.5 text-meta text-muted transition-colors hover:border-accent hover:text-text"
          >
            Max
          </button>
        </div>

        {perPercentMove ? (
          <p className="mt-2 text-meta text-faint">
            A 1% move your way is worth{' '}
            <span className="tnum text-muted">{money(perPercentMove.toString())}</span>
          </p>
        ) : null}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-3">
        <LevelField
          label="Stop loss"
          enabled={slEnabled}
          onToggle={() => {
            setSlEnabled((v) => !v)
            if (slEnabled) setStopLoss('')
          }}
          value={stopLoss}
          onChange={setStopLoss}
          error={slError}
          hint={slEnabled && stopLoss ? levelHint(stopLoss) : undefined}
          placeholder={entry && symbol ? fmtPrice(entry.toString(), symbol.pip_size) : ''}
        />
        <LevelField
          label="Take profit"
          enabled={tpEnabled}
          onToggle={() => {
            setTpEnabled((v) => !v)
            if (tpEnabled) setTakeProfit('')
          }}
          value={takeProfit}
          onChange={setTakeProfit}
          error={tpError}
          hint={tpEnabled && takeProfit ? levelHint(takeProfit) : undefined}
          placeholder={entry && symbol ? fmtPrice(entry.toString(), symbol.pip_size) : ''}
        />
      </div>

      <p className="mt-3 text-meta text-muted">
        Available <span className="tnum text-text">{money(available.toString())}</span>
        {isDemo ? ' · practice funds' : null}
      </p>

      {error && !error.field ? (
        <p role="alert" className="mt-2 text-meta text-accent">
          {humanMessage(error)}
        </p>
      ) : null}

      {disabled && disabledReason ? (
        <p className="mt-2 text-meta text-warning">{disabledReason}</p>
      ) : null}

      <Button
        size="lg"
        block
        className="mt-3"
        variant={direction === 'buy' ? 'buy' : 'sell'}
        disabled={!canSubmit}
        loading={submitting}
        onClick={() => onSubmit(values)}
      >
        {direction === 'buy' ? 'Buy' : 'Sell'} {symbol?.code ?? ''} for {money(stake || '0')}
      </Button>
    </div>
  )
}

function LevelField({
  label,
  enabled,
  onToggle,
  value,
  onChange,
  error,
  hint,
  placeholder,
}: {
  label: string
  enabled: boolean
  onToggle: () => void
  value: string
  onChange: (v: string) => void
  error?: string
  hint?: string
  placeholder?: string
}) {
  // The visible label sits in a header row beside the On/Off toggle, so it
  // cannot wrap the input. It is tied to the field by id instead, which is
  // what lets a screen reader say which level is being edited.
  const fieldId = useId()

  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <label htmlFor={fieldId} className="text-meta font-medium text-muted">
          {label}
        </label>
        <button
          type="button"
          onClick={onToggle}
          aria-pressed={enabled}
          aria-label={`${enabled ? 'Remove' : 'Set'} ${label.toLowerCase()}`}
          className={cn(
            'rounded px-1.5 py-0.5 text-nano font-semibold uppercase tracking-wider transition-colors',
            enabled ? 'bg-accent-subtle text-accent' : 'bg-elevated text-muted hover:text-text',
          )}
        >
          {enabled ? 'On' : 'Off'}
        </button>
      </div>

      {enabled ? (
        <Input
          id={fieldId}
          type="text"
          inputMode="decimal"
          value={value}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value.replace(/[^\d.]/g, ''))}
          error={error}
          hint={hint}
        />
      ) : (
        // Off is a level not yet set, not a feature switched off, so it reads as
        // a control waiting to be used: a solid border and a plus, not a dashed
        // grey box. One tap away, in the ticket, with no navigation.
        <button
          type="button"
          id={fieldId}
          onClick={onToggle}
          className={cn(
            'flex h-11 w-full items-center gap-1.5 rounded-xl border border-border bg-elevated/40 px-3',
            'text-body text-muted transition-colors hover:border-accent hover:text-text',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            'focus-visible:ring-offset-2 focus-visible:ring-offset-surface',
          )}
        >
          <Plus className="h-3.5 w-3.5 shrink-0" aria-hidden />
          Set a level
        </button>
      )}
    </div>
  )
}
