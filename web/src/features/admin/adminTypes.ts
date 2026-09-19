import type { Account, KYCStatus, Transaction, Trade, User, Wallet } from '@/types'

export type AdminRole = 'admin' | 'support' | 'restricted'

export interface AdminUser {
  id: string
  username: string
  role: AdminRole
  must_change_password: boolean
  created_at: string
}

export interface AdminLoginResult {
  admin: AdminUser
  access_token: string
  expires_in: number
  password_change_required: boolean
}

export interface AccountDrift {
  account_id: string
  account_number: string
  ledger_total: string
  cached_total: string
  difference: string
}

export interface HourlyDeposits {
  hour: string
  completed: number
  failed: number
}

export interface Dashboard {
  users: number
  deposits_today: number
  deposits_today_usd: string
  withdrawals_today: number
  withdrawals_today_usd: string
  open_positions: number
  live_connections: number
  pending_kyc: number
  /** Accounts whose ledger and cached balance disagree. Healthy is zero. */
  drifted_accounts: number
  drift: AccountDrift[]
  deposits_by_hour: HourlyDeposits[]
}

export interface AdminUserRow extends User {
  accounts: Account[]
  real_balance: string
  total_balance: string
}

export interface AdminAccountDetail extends Account {
  wallet: Wallet | null
}

export interface AdminUserDetail {
  user: User
  accounts: AdminAccountDetail[]
  transactions: Transaction[]
  open_trades: Trade[]
  kyc_submissions: KYCSubmission[]
  audit: AuditEntry[]
}

export interface AdminTransaction extends Transaction {
  account_number: string
  account_type: string
  user_id: string
  user_phone: string
  admin_username: string | null
}

export interface KYCSubmission {
  id: string
  user_id: string
  id_number: string
  document_url: string
  status: 'pending' | 'approved' | 'rejected'
  reviewed_by: string | null
  review_reason: string | null
  submitted_at: string
  reviewed_at: string | null
  user_phone: string
  user_full_name: string | null
  reviewer_name: string | null
}

export type AuditAction =
  | 'freeze_account'
  | 'unfreeze_account'
  | 'manual_adjustment'
  | 'kyc_approve'
  | 'kyc_reject'

export interface AuditEntry {
  id: string
  admin_id: string
  action: AuditAction
  target_type: 'user' | 'account' | 'transaction'
  target_id: string
  reason: string
  metadata: Record<string, unknown> | null
  created_at: string
  admin_username: string
}

export type { KYCStatus }
