import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { authApi, type RegisterInput } from '@/api/auth'
import { useSessionStore } from '@/store/session'

export function useLogin() {
  const setSession = useSessionStore((s) => s.setSession)
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ phone, password }: { phone: string; password: string }) =>
      authApi.login(phone, password),
    onSuccess: (result) => {
      setSession(result, result.user)
      // Nothing cached belongs to the previous session.
      queryClient.clear()
    },
  })
}

export function useRegister() {
  const setSession = useSessionStore((s) => s.setSession)
  const setActiveAccount = useSessionStore((s) => s.setActiveAccount)
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (input: RegisterInput) => authApi.register(input),
    onSuccess: (result) => {
      setSession(result, result.user)
      // Register returns the demo account it created, so the app lands on a
      // usable account with no second round trip.
      if (result.account) setActiveAccount(result.account.id)
      queryClient.clear()
    },
  })
}

export function useLogout() {
  const clear = useSessionStore((s) => s.clear)
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  return async () => {
    // Clear the cookie server-side first. Without this the browser keeps a
    // valid refresh credential and the next boot silently signs back in.
    try {
      await authApi.logout()
    } catch {
      // A failed logout call must not trap the user in a signed-in state.
    }
    clear()
    queryClient.clear()
    navigate('/login', { replace: true })
  }
}
