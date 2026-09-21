import { Suspense, lazy, useEffect, useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { ApiError, bootstrapSession } from '@/api/client'
import { Spinner } from '@/components/Spinner'
import { Toaster } from '@/components/ui/Toast'
import { LoginScreen } from '@/features/auth/LoginScreen'
import { RegisterScreen } from '@/features/auth/RegisterScreen'
import { HomeScreen } from '@/features/home/HomeScreen'
import { ProfileScreen } from '@/features/profile/ProfileScreen'
import { AppShell } from '@/features/shell/AppShell'
import { SignalsScreen } from '@/features/signals/SignalsScreen'
import { TradeScreen } from '@/features/trading/TradeScreen'
import { WalletScreen } from '@/features/wallet/WalletScreen'

// The admin console is a separate product with its own session and layout.
// Loading it lazily keeps it out of the user app's bundle entirely.
const AdminApp = lazy(() => import('@/features/admin/AdminApp'))

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Retrying a 401, 403 or a validation failure just wastes a round trip.
      retry: (failureCount, error) => {
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false
        return failureCount < 2
      },
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    },
    mutations: { retry: false },
  },
})

export default function App() {
  // The access token is memory-only, so a reload starts without one. Trade the
  // refresh cookie for a fresh token before the router runs, or every reload
  // would bounce a signed-in user to the login screen.
  const [ready, setReady] = useState(false)
  useEffect(() => {
    bootstrapSession().finally(() => setReady(true))
  }, [])

  if (!ready) {
    return (
      <div className="flex min-h-full items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginScreen />} />
          <Route path="/register" element={<RegisterScreen />} />

          <Route element={<AppShell />}>
            <Route path="/" element={<HomeScreen />} />
            <Route path="/trade" element={<TradeScreen />} />
            <Route path="/portfolio" element={<Navigate to="/trade?panel=positions" replace />} />
            <Route path="/wallet" element={<WalletScreen />} />
            <Route path="/signals" element={<SignalsScreen />} />
            <Route path="/profile" element={<ProfileScreen />} />
          </Route>

          <Route
            path="/admin/*"
            element={
              <Suspense
                fallback={
                  <div className="flex min-h-full items-center justify-center">
                    <Spinner className="h-6 w-6" />
                  </div>
                }
              >
                <AdminApp />
              </Suspense>
            }
          />

          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
      <Toaster />
    </QueryClientProvider>
  )
}
