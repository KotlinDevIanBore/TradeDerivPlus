import { LogOut, ShieldCheck } from 'lucide-react'
import { AccountPill } from '@/components/AccountPill'
import { StatusBadge } from '@/components/StatusBadge'
import { Button } from '@/components/ui/Button'
import { Card, CardTitle } from '@/components/ui/Card'
import { Skeleton } from '@/components/ui/Skeleton'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { useLogout } from '@/hooks/useAuth'
import { useMe } from '@/hooks/useQueries'

const kycCopy: Record<string, string> = {
  pending: 'Verify your identity to enable withdrawals.',
  verified: 'Your identity is verified. Withdrawals are enabled.',
  rejected: 'We could not verify your identity. Contact support to try again.',
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2.5">
      <span className="text-meta text-muted">{label}</span>
      <span className="text-body text-text">{value}</span>
    </div>
  )
}

export function AccountPanel() {
  const me = useMe()
  const { accounts, isLoading } = useActiveAccount()
  const logout = useLogout()

  const user = me.data
  const kyc = user?.kyc_status ?? 'pending'

  return (
    <div className="space-y-3 py-2">
      <Card>
        {me.isLoading ? (
          <div className="space-y-3">
            <Skeleton className="h-5 w-40" />
            <Skeleton className="h-4 w-32" />
          </div>
        ) : (
          <div className="divide-y divide-hairline">
            <Row label="Name" value={user?.full_name ?? 'Not set'} />
            <Row label="Phone" value={<span className="tnum">+{user?.phone_number}</span>} />
            <Row label="Email" value={user?.email ?? 'Not set'} />
          </div>
        )}
      </Card>

      <Card>
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle>Identity verification</CardTitle>
            <p className="mt-1 max-w-[40ch] text-meta text-muted">{kycCopy[kyc]}</p>
          </div>
          <StatusBadge status={kyc === 'verified' ? 'completed' : kyc === 'rejected' ? 'failed' : 'pending'} />
        </div>

        {kyc !== 'verified' ? (
          <Button variant="outline" block className="mt-4" disabled>
            <ShieldCheck className="h-4 w-4" aria-hidden />
            Start verification
          </Button>
        ) : null}
        {kyc !== 'verified' ? (
          <p className="mt-2 text-meta text-faint">Verification is not available yet.</p>
        ) : null}
      </Card>

      <Card>
        <CardTitle>Accounts</CardTitle>
        <div className="mt-3 space-y-2">
          {isLoading ? (
            <>
              <Skeleton className="h-6 w-32" />
              <Skeleton className="h-6 w-32" />
            </>
          ) : (
            accounts.map((account) => (
              <div key={account.id} className="flex items-center justify-between">
                <AccountPill account={account} />
                <span className="text-meta text-muted">{account.currency}</span>
              </div>
            ))
          )}
        </div>
      </Card>

      <Button variant="danger" block onClick={logout}>
        <LogOut className="h-4 w-4" aria-hidden />
        Log out
      </Button>
    </div>
  )
}
