import { api } from './client'
import type { AccessGrant, AuthResult, User } from '@/types'

export interface RegisterInput {
  full_name?: string
  phone_number: string
  email?: string
  password: string
}

export const authApi = {
  register: (input: RegisterInput) =>
    api.post<AuthResult>('/auth/register', input, { anonymous: true }),

  login: (phone_number: string, password: string) =>
    api.post<AuthResult>('/auth/login', { phone_number, password }, { anonymous: true }),

  /** Reads the httpOnly refresh cookie; there is no token to pass. */
  refresh: () => api.post<AccessGrant>('/auth/refresh', undefined, { anonymous: true }),

  /** Clears the refresh cookie server-side. */
  logout: () => api.post<void>('/auth/logout'),

  me: () => api.get<User>('/auth/me'),
}
