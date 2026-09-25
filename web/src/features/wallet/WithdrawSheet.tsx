import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ShieldAlert } from 'lucide-react'
import { ApiError } from '@/api/client'
import { walletApi, type WithdrawInput } from '@/api/wallet'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Sheet } from '@/components/ui/Sheet'
import { humanMessage } from '@/components/ErrorState'
import { toast } from '@/components/ui/Toast'
import { queryKeys } from '@/lib/queryKeys'
import { kes, money } from '@/lib/format'
import { dec } from '@/lib/money'
import { useRate } from '@/hooks/useQueries'
import { useSessionStore } from '@/store/session'
import type { Account, Wallet } from '@/types'

/**
 * Withdrawals are real balance only, and only after verification. Bonus money
 * is never withdrawable, which the sheet says plainly rather than silently
 * capping the amount.
 */
export function WithdrawSheet({
  open,
  onOpenChange,
  account,
  wallet,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  account: Account | null
  wallet?: Wallet
}) {
  const user = useSessionStore((s) => s.user)
  const queryClient = useQueryClient()
  const rate = useRate()

  const [amount, setAmount] = useState('')
  const [phone, setPhone] = useState('')
  const [confirming, setConfirming] = useState(false)

  useEffect(() => {
    if (open && user?.phone_number) setPhone(user.phone_number.replace(/^254/, ''))
    if (!open) {
      setAmount('')
      setConfirming(false)
    }
  }, [open, user?.phone_number])

  const withdraw = useMutation({
    mutationFn: (input: WithdrawInput) => walletApi.withdraw(input),
    onSuccess: (tx) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.wallet(tx.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.transactions(tx.account_id) })
      // What happens, not when. The server sends the request to M-Pesa and then
      // waits on M-Pesa's own confirmation, which is usually quick and sometimes
      // is not; and if it does not go through, the money comes back on its own.
      // Promising minutes was a promise nothing could keep, and the previous copy
      // made it twice.
      toast.success('Sent to M-Pesa', "We'll tell you as soon as M-Pesa confirms it.")
      onOpenChange(false)
    },
  })

  const available = dec(wallet?.real_balance)
  const amountValue = dec(amount)
  const error = withdraw.error as ApiError | null

  const amountError =
    amount !== '' && amountValue.greaterThan(available)
      ? `You can withdraw up to ${money(available.toString())}`
      : amount !== '' && amountValue.lessThanOrEqualTo(0)
        ? 'Enter an amount greater than zero'
        : undefined

  const phoneError =
    phone !== '' && !/^\d{9}$/.test(phone.replace(/\D/g, '')) ? 'Enter a valid Safaricom number' : undefined

  const canSubmit = !amountError && !phoneError && amount !== '' && phone !== ''
  const kesAmount = rate.data ? amountValue.times(rate.data.kes_per_usd).toFixed(0) : null

  // Withdrawals are off until the M-Pesa payout exists. Stated plainly rather
  // than left as a form that debits the balance and pays nobody, and checked
  // before the verification gate: while the feature is off, being verified
  // would not help, so promising that it would is the wrong thing to say.
  if (user?.withdrawals_enabled === false) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="Withdraw to M-Pesa">
        <div className="py-6 text-center">
          <h3 className="text-title font-bold tracking-tight">Not available yet</h3>
          <p className="mx-auto mt-2 max-w-[34ch] text-body text-muted">
            We can't send money out to M-Pesa yet. Your balance stays where it is, and we'll turn
            this on as soon as payouts are ready.
          </p>
          <Button variant="ghost" block className="mt-6" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </div>
      </Sheet>
    )
  }

  if (user && user.kyc_status !== 'verified') {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="Verification required">
        <div className="py-2 text-center">
          <ShieldAlert className="mx-auto h-12 w-12 text-warning" aria-hidden />
          <p className="mt-4 text-body text-text">Verify your identity to withdraw</p>
          <p className="mx-auto mt-2 max-w-[34ch] text-meta text-muted">
            We need to confirm who you are before sending money out. This protects your account.
          </p>
          <Button block className="mt-6" disabled>
            Start verification
          </Button>
          <p className="mt-2 text-meta text-faint">Verification is not available yet.</p>
        </div>
      </Sheet>
    )
  }

  if (confirming) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="Confirm withdrawal">
        <p className="text-body text-muted">
          Send <span className="font-semibold text-text">{money(amount)}</span>
          {kesAmount ? <> ({kes(kesAmount)})</> : null} to{' '}
          <span className="font-semibold text-text tnum">+254 {phone}</span>?
        </p>

        {error ? (
          <p role="alert" className="mt-3 text-meta text-accent">
            {humanMessage(error)}
          </p>
        ) : null}

        <div className="mt-6 space-y-2">
          <Button
            size="lg"
            block
            loading={withdraw.isPending}
            onClick={() =>
              account &&
              withdraw.mutate({
                account_id: account.id,
                amount_usd: amountValue.toFixed(2),
                phone_number: phone,
              })
            }
          >
            Withdraw {money(amount)}
          </Button>
          <Button variant="ghost" block onClick={() => setConfirming(false)}>
            Back
          </Button>
        </div>
      </Sheet>
    )
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="Withdraw to M-Pesa">
      <div className="space-y-4">
        <Input
          label="Amount"
          type="text"
          inputMode="decimal"
          prefix="$"
          placeholder="0.00"
          value={amount}
          onChange={(e) => setAmount(e.target.value.replace(/[^\d.]/g, ''))}
          error={amountError}
          hint={kesAmount && amountValue.greaterThan(0) ? `≈ ${kes(kesAmount)}` : undefined}
          className="text-title font-semibold"
          trailing={
            <button
              type="button"
              onClick={() => setAmount(available.toFixed(2))}
              className="rounded-md bg-elevated px-2 py-1 text-meta font-semibold text-accent"
            >
              Max
            </button>
          }
          autoFocus
        />

        <p className="text-meta text-muted">
          Available {money(available.toString())}.{' '}
          {dec(wallet?.bonus_balance).greaterThan(0) ? "Bonus balance can't be withdrawn." : null}
        </p>

        <Input
          label="M-Pesa number"
          type="tel"
          inputMode="numeric"
          prefix="+254"
          placeholder="712 345 678"
          value={phone}
          onChange={(e) => setPhone(e.target.value.replace(/[^\d]/g, '').slice(0, 9))}
          error={phoneError}
        />

        {/* No fee is a fact. How long it takes is not ours to promise: the payout
            is confirmed by M-Pesa, on M-Pesa's own timing, and a request that
            stalls can take hours rather than minutes. What is worth saying is
            what happens if it fails, because that is the part a customer worries
            about while their balance is already down. */}
        <p className="text-meta text-faint">
          No fee. If it doesn't go through, the money returns to your balance.
        </p>

        <Button size="lg" block disabled={!canSubmit} onClick={() => setConfirming(true)}>
          Withdraw
        </Button>
      </div>
    </Sheet>
  )
}
