import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { humanMessage } from '@/components/ErrorState'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { adminApi } from '../adminApi'
import { useAdminSession } from '../adminSession'

/**
 * Deliberately plain and unbranded relative to the user login, so nobody
 * mistakes one surface for the other.
 */
export function AdminLogin() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const setSession = useAdminSession((s) => s.setSession)

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [pending, setPending] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username || !password) return

    setPending(true)
    setError(null)
    try {
      const result = await adminApi.login(username, password)
      setSession(result.access_token, result.admin, result.password_change_required)
      navigate(
        result.password_change_required ? '/admin/change-password' : (params.get('next') ?? '/admin'),
        { replace: true },
      )
    } catch (err) {
      setError(err)
    } finally {
      setPending(false)
    }
  }

  const fieldError = (field: string) =>
    error instanceof ApiError && error.field === field ? error.message : undefined

  return (
    <div className="flex min-h-full items-center justify-center px-gutter py-10">
      <div className="w-full max-w-[380px]">
        <div className="mb-8">
          <h1 className="text-title font-bold tracking-tight">Admin sign in</h1>
          <p className="mt-1 text-body text-muted">TradeDerivPlus operations console</p>
        </div>

        <form onSubmit={submit} noValidate className="space-y-4">
          <Input
            label="Username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            error={fieldError('username')}
            disabled={pending}
            autoFocus
          />
          <Input
            label="Password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            error={fieldError('password')}
            disabled={pending}
          />

          {error && !(error instanceof ApiError && error.field) ? (
            <p role="alert" className="text-meta text-accent">
              {humanMessage(error)}
            </p>
          ) : null}

          <Button type="submit" size="lg" block loading={pending}>
            Sign in
          </Button>
        </form>
      </div>
    </div>
  )
}
