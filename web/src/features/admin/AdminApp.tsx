import { useEffect, useState } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { Spinner } from '@/components/Spinner'
import { bootstrapAdminSession } from './adminApi'
import { AdminShell } from './AdminShell'
import { AdminAuditLog } from './screens/AuditLog'
import { AdminBotRuns } from './screens/BotRuns'
import { AdminDashboard } from './screens/Dashboard'
import { AdminKYCQueue } from './screens/KYCQueue'
import { AdminLogin } from './screens/AdminLogin'
import { AdminPayouts } from './screens/Payouts'
import { AdminTransactions } from './screens/Transactions'
import { AdminUserDetailScreen } from './screens/UserDetail'
import { AdminUsers } from './screens/Users'
import { ChangePassword } from './screens/ChangePassword'

/**
 * The whole admin console, mounted lazily so the user app's bundle does not
 * carry it. Its session is bootstrapped from the admin refresh cookie the same
 * way the user app's is, and independently of it.
 */
export default function AdminApp() {
  const [ready, setReady] = useState(false)

  useEffect(() => {
    bootstrapAdminSession().finally(() => setReady(true))
  }, [])

  if (!ready) {
    return (
      <div className="flex min-h-full items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  return (
    <Routes>
      <Route path="login" element={<AdminLogin />} />

      <Route element={<AdminShell />}>
        <Route index element={<AdminDashboard />} />
        <Route path="change-password" element={<ChangePassword />} />
        <Route path="users" element={<AdminUsers />} />
        <Route path="users/:id" element={<AdminUserDetailScreen />} />
        <Route path="transactions" element={<AdminTransactions />} />
        <Route path="kyc" element={<AdminKYCQueue />} />
        <Route path="payouts" element={<AdminPayouts />} />
        <Route path="bot-runs" element={<AdminBotRuns />} />
        <Route path="audit-log" element={<AdminAuditLog />} />
      </Route>

      <Route path="*" element={<Navigate to="/admin" replace />} />
    </Routes>
  )
}
