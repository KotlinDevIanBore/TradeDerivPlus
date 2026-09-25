import type { Account, AccountType, BotRun, KYCStatus, Trade, Transaction, TxStatus, User, Wallet } from '@/types'

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
  /** Runs placing contracts right now, across every account. */
  live_bot_runs: number
  /** The global switch, read from its row rather than through the settings cache. */
  bot_enabled: boolean
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
  /** An R2 object key, not a URL. Ask the API for a short-lived signed URL to view it. */
  document_key: string
  content_type: string
  size_bytes: number
  status: 'pending' | 'approved' | 'rejected'
  reviewed_by: string | null
  review_reason: string | null
  submitted_at: string
  reviewed_at: string | null
  user_phone: string
  user_full_name: string | null
  reviewer_name: string | null
}

/**
 * A payout as the console sees it. `status` is the payout's own lifecycle;
 * `ledger_status` is the withdrawal's. They differ while a payout is in flight,
 * which is the whole reason this screen exists.
 */
export interface AdminPayout {
  id: string
  transaction_id: string
  originator_conversation_id: string
  conversation_id: string | null
  phone_number: string
  amount: string
  status: 'pending' | 'sent' | 'unknown' | 'never_received' | 'completed' | 'failed'
  requested_at: string | null
  result_code: string | null
  result_desc: string | null
  receipt: string | null
  settled_by: string | null
  reconcile_attempts: number
  next_query_at: string | null
  created_at: string
  updated_at: string
  account_number: string
  account_type: AccountType
  user_id: string
  user_phone: string
  ledger_status: TxStatus
  /** Open, and nothing is looking at it any more. */
  given_up: boolean
}

export type AuditAction =
  | 'freeze_account'
  | 'unfreeze_account'
  | 'manual_adjustment'
  | 'kyc_approve'
  | 'kyc_reject'
  | 'bot_kill'
  | 'bot_enable'
  | 'bot_run_stop'
  | 'deposit_resolve'
  | 'payout_resolve'

/**
 * A run as the console sees it: the run row plus whose it is. The user app's
 * `BotRun` is the same row without the owner — a user polling their own run does
 * not need telling whose it is, and the admin read path is the only one that
 * joins for it.
 */
export interface AdminBotRun extends BotRun {
  account_number: string
  account_type: AccountType
  user_id: string
  user_phone: string
}

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
