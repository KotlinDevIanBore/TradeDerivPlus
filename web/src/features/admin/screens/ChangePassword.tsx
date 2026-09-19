import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Check } from 'lucide-react'
import { ApiError } from '@/api/client'
import { humanMessage } from '@/components/ErrorState'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { cn } from '@/lib/cn'
import { adminApi } from '../adminApi'
import { useAdminSession } from '../adminSession'

const SEEDED = 'admin123'

export function ChangePassword() {
  const navigate = useNavigate()
  const { admin, passwordChangeRequired, clear } = useAdminSession()

  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [pending, setPending] = useState(false)

  const rules = [
    { label: 'At least 8 characters', met: next.length >= 8 },
    { label: 'Contains a number', met: /\d/.test(next) },
    { label: 'Not the default password', met: next !== SEEDED && next.length > 0 },
  ]
  const valid = rules.every((r) => r.met) && next === confirm && current.length > 0

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!valid) return

    setPending(true)
    setError(null)
    try {
      await adminApi.changePassword(current, next)
      // The old token still carries the restricted role, so the session is
      // ended rather than left half-privileged.
      clear()
      navigate('/admin/login', { replace: true })
    } catch (err) {
      setError(err)
    } finally {
      setPending(false)
    }
  }

  const fieldError = (field: string) =>
    error instanceof ApiError && error.field === field ? error.message : undefined

  return (
    <div className="mx-auto max-w-[420px] py-4">
      <h1 className="text-title font-bold tracking-tight">Change your password</h1>
      <p className="mt-1 text-body text-muted">
        {passwordChangeRequired
          ? 'The console is locked until the default password is replaced.'
          : admin?.must_change_password
            ? 'You are still on the seeded default password. Replace it.'
            : 'You will be signed out and asked to sign in again.'}
      </p>

      <form onSubmit={submit} noValidate className="mt-6 space-y-4">
        <Input
          label="Current password"
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          error={fieldError('current_password')}
          disabled={pending}
          autoFocus
        />

        <div>
          <Input
            label="New password"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            error={fieldError('new_password')}
            disabled={pending}
          />
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
          label="Confirm new password"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          error={confirm && confirm !== next ? 'Passwords do not match' : undefined}
          disabled={pending}
        />

        {error && !(error instanceof ApiError && error.field) ? (
          <p role="alert" className="text-meta text-accent">
            {humanMessage(error)}
          </p>
        ) : null}

        <Button type="submit" size="lg" block disabled={!valid} loading={pending}>
          Change password
        </Button>
      </form>
    </div>
  )
}
