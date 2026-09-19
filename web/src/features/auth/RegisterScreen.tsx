import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Check } from 'lucide-react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { humanMessage } from '@/components/ErrorState'
import { useRegister } from '@/hooks/useAuth'
import { cn } from '@/lib/cn'
import { AuthLayout } from './AuthLayout'

/** The two rules the server enforces, shown as a checklist that ticks live. */
function passwordRules(password: string) {
  return [
    { label: 'At least 8 characters', met: password.length >= 8 },
    { label: 'Contains a number', met: /\d/.test(password) },
  ]
}

function strength(password: string) {
  const met = passwordRules(password).filter((r) => r.met).length
  if (password.length === 0) return 0
  if (met < 2) return 1
  return password.length >= 12 ? 3 : 2
}

export function RegisterScreen() {
  const navigate = useNavigate()
  const register = useRegister()

  const [form, setForm] = useState({ fullName: '', phone: '', email: '', password: '', confirm: '' })
  const [agreed, setAgreed] = useState(false)
  const [touched, setTouched] = useState<Record<string, boolean>>({})

  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [key]: e.target.value }))
  const blur = (key: string) => () => setTouched((t) => ({ ...t, [key]: true }))

  const rules = passwordRules(form.password)
  const meter = strength(form.password)

  const phoneError =
    touched.phone && !/^\d{9,12}$/.test(form.phone.replace(/\D/g, ''))
      ? 'Enter a valid Safaricom number'
      : undefined
  const confirmError =
    touched.confirm && form.confirm !== form.password ? 'Passwords do not match' : undefined

  const apiError = register.error as ApiError | null
  const fieldError = (field: string) => (apiError?.field === field ? apiError.message : undefined)

  const canSubmit =
    rules.every((r) => r.met) && form.confirm === form.password && form.phone !== '' && agreed

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    setTouched({ fullName: true, phone: true, email: true, password: true, confirm: true })
    if (!canSubmit) return

    register.mutate(
      {
        full_name: form.fullName || undefined,
        phone_number: form.phone,
        email: form.email || undefined,
        password: form.password,
      },
      { onSuccess: () => navigate('/', { replace: true }) },
    )
  }

  return (
    <AuthLayout
      title="Create your account"
      subtitle="Start on a demo account with $10,000 to practise"
      footer={
        <>
          Already have an account?{' '}
          <Link to="/login" className="font-semibold text-accent hover:underline">
            Log in
          </Link>
        </>
      }
    >
      <form onSubmit={submit} noValidate className="space-y-4">
        <Input
          label="Full name"
          autoComplete="name"
          placeholder="Ian Bore"
          value={form.fullName}
          onChange={set('fullName')}
          disabled={register.isPending}
        />

        <Input
          label="Phone number"
          type="tel"
          inputMode="numeric"
          autoComplete="tel"
          prefix="+254"
          placeholder="712 345 678"
          value={form.phone}
          onChange={set('phone')}
          onBlur={blur('phone')}
          error={phoneError ?? fieldError('phone_number')}
          disabled={register.isPending}
        />

        <Input
          label="Email (optional)"
          type="email"
          autoComplete="email"
          placeholder="you@example.com"
          value={form.email}
          onChange={set('email')}
          error={fieldError('email')}
          disabled={register.isPending}
        />

        <div>
          <Input
            label="Password"
            type="password"
            autoComplete="new-password"
            placeholder="At least 8 characters"
            value={form.password}
            onChange={set('password')}
            onBlur={blur('password')}
            error={fieldError('password')}
            disabled={register.isPending}
          />

          <div className="mt-2 flex gap-1" aria-hidden>
            {[1, 2, 3].map((i) => (
              <span
                key={i}
                className={cn(
                  'h-1 flex-1 rounded-full transition-colors',
                  meter >= i
                    ? meter === 1
                      ? 'bg-accent'
                      : meter === 2
                        ? 'bg-warning'
                        : 'bg-profit'
                    : 'bg-border',
                )}
              />
            ))}
          </div>

          <ul className="mt-2 space-y-1">
            {rules.map((r) => (
              <li
                key={r.label}
                className={cn('flex items-center gap-1.5 text-meta', r.met ? 'text-profit' : 'text-faint')}
              >
                <Check className={cn('h-3 w-3', !r.met && 'opacity-30')} aria-hidden />
                {r.label}
              </li>
            ))}
          </ul>
        </div>

        <Input
          label="Confirm password"
          type="password"
          autoComplete="new-password"
          value={form.confirm}
          onChange={set('confirm')}
          onBlur={blur('confirm')}
          error={confirmError}
          disabled={register.isPending}
        />

        <label className="flex items-start gap-2.5 text-meta text-muted">
          <input
            type="checkbox"
            checked={agreed}
            onChange={(e) => setAgreed(e.target.checked)}
            className="mt-0.5 h-4 w-4 shrink-0 accent-[#E24B4A]"
          />
          <span>
            I agree to the terms of service and understand that trading carries risk of loss.
          </span>
        </label>

        {apiError && !apiError.field ? (
          <p role="alert" className="text-meta text-accent">
            {humanMessage(apiError)}
          </p>
        ) : null}

        {apiError?.code === 'phone_taken' ? (
          <p className="text-meta text-muted">
            <Link to="/login" className="font-semibold text-accent hover:underline">
              Log in instead
            </Link>
          </p>
        ) : null}

        <Button type="submit" size="lg" block loading={register.isPending} disabled={!canSubmit}>
          Create account
        </Button>
      </form>
    </AuthLayout>
  )
}
