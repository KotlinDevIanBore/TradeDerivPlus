import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, XCircle } from 'lucide-react'
import { ApiError } from '@/api/client'
import { walletApi } from '@/api/wallet'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Sheet } from '@/components/ui/Sheet'
import { Spinner } from '@/components/Spinner'
import { humanMessage } from '@/components/ErrorState'
import { queryKeys } from '@/lib/queryKeys'
import { kes, money } from '@/lib/format'
import { dec } from '@/lib/money'
import { useRate } from '@/hooks/useQueries'
import { useSessionStore } from '@/store/session'
import type { Account, Transaction } from '@/types'
import { CountdownRing } from './CountdownRing'

type Step = 'amount' | 'sending' | 'pending' | 'success' | 'failed' | 'timeout'

const QUICK_AMOUNTS = [500, 1000, 2000, 5000]
const PIN_WINDOW_SECONDS = 60
const GIVE_UP_AFTER_MS = 120_000

/**
 * The M-Pesa deposit flow: amount, sending, waiting for the PIN prompt, result.
 *
 * Settlement arrives over the account socket. A poll every three seconds runs
 * alongside it as a fallback, because a user whose socket dropped still needs
 * to see their deposit land.
 */
export function DepositSheet({
  open,
  onOpenChange,
  account,
  realAccount,
  onSwitchToReal,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  account: Account | null
  /** The user's real account, if they have one. */
  realAccount: Account | null
  onSwitchToReal: () => void
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const user = useSessionStore((s) => s.user)
  const rate = useRate()

  const [step, setStep] = useState<Step>('amount')
  const [amount, setAmount] = useState('')
  const [phone, setPhone] = useState('')
  const [error, setError] = useState<ApiError | null>(null)
  const [settled, setSettled] = useState<Transaction | null>(null)
  const transactionRef = useRef<string | null>(null)

  const isDemo = account?.account_type === 'demo'

  // Prefill the phone from the profile each time the sheet opens.
  useEffect(() => {
    if (open && user?.phone_number) {
      setPhone(user.phone_number.replace(/^254/, ''))
    }
  }, [open, user?.phone_number])

  // Reset when the sheet closes, so reopening never resumes a finished flow.
  useEffect(() => {
    if (open) return
    const id = setTimeout(() => {
      setStep('amount')
      setError(null)
      setSettled(null)
      transactionRef.current = null
    }, 250)
    return () => clearTimeout(id)
  }, [open])

  const amountValue = dec(amount)
  const usd = useMemo(() => {
    const perUsd = rate.data?.kes_per_usd ?? 0
    if (!perUsd || amountValue.isZero()) return null
    return amountValue.dividedBy(perUsd).toFixed(2)
  }, [amountValue, rate.data])

  const amountError =
    amount !== '' && amountValue.lessThan(50) ? 'Minimum deposit is KES 50' : undefined
  const phoneError =
    phone !== '' && !/^\d{9}$/.test(phone.replace(/\D/g, '')) ? 'Enter a valid Safaricom number' : undefined
  const canSubmit = !amountError && !phoneError && amount !== '' && phone !== ''

  // Poll the transaction while pending, as a fallback to the account socket.
  useEffect(() => {
    if (step !== 'pending' || !transactionRef.current) return

    const startedAt = Date.now()
    const id = setInterval(async () => {
      if (Date.now() - startedAt > GIVE_UP_AFTER_MS) {
        clearInterval(id)
        setStep('timeout')
        return
      }
      try {
        const tx = await walletApi.transaction(transactionRef.current!)
        if (tx.status === 'pending') return

        clearInterval(id)
        setSettled(tx)
        setStep(tx.status === 'completed' ? 'success' : 'failed')
        queryClient.invalidateQueries({ queryKey: queryKeys.wallet(tx.account_id) })
        queryClient.invalidateQueries({ queryKey: queryKeys.transactions(tx.account_id) })
      } catch {
        // A failed poll is not fatal; the next tick tries again.
      }
    }, 3000)

    return () => clearInterval(id)
  }, [step, queryClient])

  const submit = async () => {
    if (!realAccount || !canSubmit) return
    setStep('sending')
    setError(null)

    try {
      const res = await walletApi.deposit({
        account_id: realAccount.id,
        amount_kes: amountValue.toFixed(0),
        phone_number: phone,
      })
      transactionRef.current = res.transaction_id
      setStep('pending')
    } catch (e) {
      setError(e as ApiError)
      setStep('amount')
    }
  }

  // Deposits are real money only; a demo account gets a way across instead.
  if (isDemo && !realAccount) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="Deposit">
        <p className="text-body text-muted">
          Deposits go to a real account. Create one to fund your trading.
        </p>
        <Button block className="mt-5" onClick={onSwitchToReal}>
          Create a real account
        </Button>
      </Sheet>
    )
  }

  if (isDemo && realAccount) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="Deposit">
        <p className="text-body text-muted">
          You're on your demo account. Switch to your real account to deposit.
        </p>
        <Button block className="mt-5" onClick={onSwitchToReal}>
          Switch to real account
        </Button>
      </Sheet>
    )
  }

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={step === 'amount' ? 'Deposit with M-Pesa' : undefined}
      hideTitle={step !== 'amount'}
      showClose={step !== 'sending'}
    >
      {step === 'amount' ? (
        <div className="space-y-4">
          <Input
            label="Amount"
            type="text"
            inputMode="numeric"
            prefix="KES"
            placeholder="1,000"
            value={amount}
            onChange={(e) => setAmount(e.target.value.replace(/[^\d]/g, ''))}
            error={amountError ?? (error?.field === 'amount' ? error.message : undefined)}
            hint={usd ? `≈ ${money(usd)} will be credited` : undefined}
            className="text-title font-semibold"
            autoFocus
          />

          <div className="flex gap-2">
            {QUICK_AMOUNTS.map((v) => (
              <button
                key={v}
                type="button"
                onClick={() => setAmount(String(v))}
                className="flex-1 rounded-lg border border-border py-2 text-meta text-muted transition-colors hover:border-accent hover:text-text"
              >
                {v.toLocaleString()}
              </button>
            ))}
          </div>

          <Input
            label="M-Pesa number"
            type="tel"
            inputMode="numeric"
            prefix="+254"
            placeholder="712 345 678"
            value={phone}
            onChange={(e) => setPhone(e.target.value.replace(/[^\d]/g, '').slice(0, 9))}
            error={phoneError ?? (error?.field === 'phone_number' ? error.message : undefined)}
          />

          {error && !error.field ? (
            <p role="alert" className="text-meta text-accent">
              {humanMessage(error)}
            </p>
          ) : null}

          <Button size="lg" block disabled={!canSubmit} onClick={submit}>
            Send STK push
          </Button>
        </div>
      ) : null}

      {step === 'sending' ? (
        <div className="flex flex-col items-center py-12">
          <Spinner className="h-6 w-6" />
          <p className="mt-4 text-body text-muted">Sending request to M-Pesa…</p>
        </div>
      ) : null}

      {step === 'pending' ? (
        <div className="py-4 text-center">
          <CountdownRing seconds={PIN_WINDOW_SECONDS} />

          <h3 className="mt-5 text-title font-bold tracking-tight">Check your phone</h3>
          <p className="mx-auto mt-2 max-w-[32ch] text-body text-muted">
            Enter your M-Pesa PIN to complete the {kes(amount)} deposit.
          </p>

          <p className="mt-4 text-meta text-faint tnum">
            Ref {transactionRef.current?.slice(0, 8)}
          </p>

          <Button variant="ghost" block className="mt-6" onClick={() => onOpenChange(false)}>
            I'll wait, close this
          </Button>
          <p className="mt-2 text-meta text-faint">
            Your balance updates automatically when the payment goes through.
          </p>
        </div>
      ) : null}

      {step === 'success' ? (
        <div className="py-6 text-center">
          <CheckCircle2 className="mx-auto h-14 w-14 text-profit" aria-hidden />
          <h3 className="mt-4 text-title font-bold tracking-tight">{kes(amount)} deposited</h3>
          <p className="mt-2 text-body text-muted">
            {money(settled?.amount)} is now in your balance.
          </p>

          <div className="mt-6 space-y-2">
            <Button
              size="lg"
              block
              onClick={() => {
                onOpenChange(false)
                navigate('/trade')
              }}
            >
              Start trading
            </Button>
            <Button variant="ghost" block onClick={() => onOpenChange(false)}>
              Done
            </Button>
          </div>
        </div>
      ) : null}

      {step === 'failed' ? (
        <div className="py-6 text-center">
          <XCircle className="mx-auto h-14 w-14 text-accent" aria-hidden />
          <h3 className="mt-4 text-title font-bold tracking-tight">Deposit didn't go through</h3>
          <p className="mx-auto mt-2 max-w-[34ch] text-body text-muted">
            {(settled?.metadata?.reason as string) ?? 'The payment was not completed.'}
          </p>

          <div className="mt-6 space-y-2">
            <Button size="lg" block onClick={() => setStep('amount')}>
              Try again
            </Button>
            <Button variant="ghost" block onClick={() => onOpenChange(false)}>
              Close
            </Button>
          </div>
        </div>
      ) : null}

      {step === 'timeout' ? (
        <div className="py-6 text-center">
          <h3 className="text-title font-bold tracking-tight">Still waiting on M-Pesa</h3>
          <p className="mx-auto mt-2 max-w-[36ch] text-body text-muted">
            We haven't heard back yet. If you completed the payment, your balance will update
            automatically.
          </p>

          <div className="mt-6 space-y-2">
            <Button size="lg" block onClick={() => setStep('amount')}>
              Try again
            </Button>
            <Button variant="ghost" block onClick={() => onOpenChange(false)}>
              Close
            </Button>
          </div>
        </div>
      ) : null}
    </Sheet>
  )
}
