import { useState } from 'react'
import { Copy, Check } from 'lucide-react'
import { Money } from '@/components/Money'
import { StatusBadge } from '@/components/StatusBadge'
import { Sheet } from '@/components/ui/Sheet'
import { absoluteTime, kes, transactionTitle } from '@/lib/format'
import type { Transaction } from '@/types'

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2">
      <span className="text-meta text-muted">{label}</span>
      <span className="text-body text-text">{value}</span>
    </div>
  )
}

export function TransactionDetail({
  transaction,
  onClose,
}: {
  transaction: Transaction | null
  onClose: () => void
}) {
  const [copied, setCopied] = useState(false)
  if (!transaction) return null

  const meta = transaction.metadata ?? {}
  const reference = (meta.receipt as string) ?? transaction.reference ?? transaction.id

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(reference)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // Clipboard access can be denied; the reference is still on screen.
    }
  }

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()} title="Transaction">
      <div className="mb-3 text-center">
        <p className="text-title font-bold">
          <Money value={transaction.amount} signed />
        </p>
        <p className="mt-1 text-body text-muted">
          {transactionTitle(transaction.type, transaction.metadata)}
        </p>
      </div>

      <div className="divide-y divide-hairline">
        <Row label="Status" value={<StatusBadge status={transaction.status} />} />
        <Row label="When" value={absoluteTime(transaction.created_at)} />
        <Row label="Balance" value={transaction.balance_type === 'bonus' ? 'Bonus' : 'Real'} />

        {meta.amount_kes ? <Row label="Charged" value={kes(meta.amount_kes as string)} /> : null}
        {meta.phone ? <Row label="Phone" value={<span className="tnum">{meta.phone as string}</span>} /> : null}
        {meta.reason ? <Row label="Result" value={meta.reason as string} /> : null}
        {meta.symbol ? <Row label="Instrument" value={meta.symbol as string} /> : null}
        {meta.pnl ? (
          <Row label="Realised" value={<Money value={meta.pnl as string} signed />} />
        ) : null}

        <div className="flex items-baseline justify-between gap-4 py-2">
          <span className="text-meta text-muted">Reference</span>
          <button
            type="button"
            onClick={copy}
            className="flex items-center gap-1.5 text-body text-text hover:text-accent"
          >
            <span className="max-w-[18ch] truncate tnum">{reference}</span>
            {copied ? (
              <Check className="h-3.5 w-3.5 text-profit" aria-hidden />
            ) : (
              <Copy className="h-3.5 w-3.5 text-muted" aria-hidden />
            )}
            <span className="sr-only">Copy reference</span>
          </button>
        </div>
      </div>
    </Sheet>
  )
}
