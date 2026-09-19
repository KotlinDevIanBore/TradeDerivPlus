import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { humanMessage } from '@/components/ErrorState'
import { useLogin } from '@/hooks/useAuth'
import { AuthLayout } from './AuthLayout'

export function LoginScreen() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const login = useLogin()

  const [phone, setPhone] = useState('')
  const [password, setPassword] = useState('')
  const [touched, setTouched] = useState({ phone: false, password: false })

  // Validate on blur, not on every keystroke.
  const phoneError =
    touched.phone && !/^\d{9,12}$/.test(phone.replace(/\D/g, ''))
      ? 'Enter a valid phone number'
      : undefined
  const passwordError = touched.password && password.length === 0 ? 'Enter your password' : undefined

  const apiError = login.error as ApiError | null
  const fieldError = (field: string) => (apiError?.field === field ? apiError.message : undefined)

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    setTouched({ phone: true, password: true })
    if (!phone || !password) return

    login.mutate(
      { phone, password },
      { onSuccess: () => navigate(params.get('next') ?? '/', { replace: true }) },
    )
  }

  return (
    <AuthLayout
      title="Welcome back"
      subtitle="Sign in to your trading account"
      footer={
        <>
          New here?{' '}
          <Link to="/register" className="font-semibold text-accent hover:underline">
            Create account
          </Link>
        </>
      }
    >
      <form onSubmit={submit} noValidate className="space-y-4">
        <Input
          label="Phone number"
          type="tel"
          inputMode="numeric"
          autoComplete="tel"
          prefix="+254"
          placeholder="712 345 678"
          value={phone}
          onChange={(e) => setPhone(e.target.value)}
          onBlur={() => setTouched((t) => ({ ...t, phone: true }))}
          error={phoneError ?? fieldError('phone_number')}
          disabled={login.isPending}
        />

        <Input
          label="Password"
          type="password"
          autoComplete="current-password"
          placeholder="Your password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onBlur={() => setTouched((t) => ({ ...t, password: true }))}
          error={passwordError ?? fieldError('password')}
          disabled={login.isPending}
        />

        {apiError && !apiError.field ? (
          <p role="alert" className="text-meta text-accent">
            {humanMessage(apiError)}
          </p>
        ) : null}

        <Button type="submit" size="lg" block loading={login.isPending}>
          Log in
        </Button>
      </form>
    </AuthLayout>
  )
}
