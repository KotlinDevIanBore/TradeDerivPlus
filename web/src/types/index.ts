// Mirrors the Go models in internal/models. Money and prices arrive as strings
// so no precision is lost to float parsing; parse them with Decimal from
// lib/money before doing arithmetic.

export type KYCStatus = 'pending' | 'verified' | 'rejected'
export type AccountType = 'demo' | 'real'
export type Direction = 'buy' | 'sell'
export type TradeStatus = 'open' | 'closed'
export type TxStatus = 'pending' | 'completed' | 'failed'
export type TxType =
  | 'deposit'
  | 'withdrawal'
  | 'trade_open'
  | 'trade_close'
  | 'digit_open'
  | 'digit_settle'
  | 'bonus_credit'
  | 'admin_adjustment'
export type SymbolCategory = 'volatility' | 'boom_crash' | 'forex'

export type DigitContractType = 'even_odd' | 'matches_differs' | 'over_under'
export type DigitContractStatus = 'open' | 'won' | 'lost' | 'void'

export interface DigitSelection {
  mode?: 'match' | 'differ'
  digit?: number
  parity?: 'even' | 'odd'
  side?: 'over' | 'under'
  barrier?: number
}

export interface DigitContract {
  id: string
  account_id: string
  symbol_id: string
  contract_type: DigitContractType
  selection: DigitSelection
  stake: string
  payout_ratio: string
  potential_payout: string
  entry_price: string
  entry_digit: number
  entry_tick_epoch: number
  status: DigitContractStatus
  settlement_price: string | null
  settlement_digit: number | null
  settled_at: string | null
  pnl: string | null
  opened_at: string
  void_after: string
  symbol_code: string
  symbol_name: string
  pip_size: number
}

export interface User {
  id: string
  phone_number: string
  email: string | null
  full_name: string | null
  kyc_status: KYCStatus
  created_at: string
  updated_at: string
  /** Runtime flag from app_settings — swaps the main trading surface. Absent on login/register payloads until /me is fetched. */
  digit_contracts_enabled?: boolean
}

export type AccountStatus = 'active' | 'frozen'

export interface Account {
  id: string
  user_id: string
  account_number: string
  account_type: AccountType
  currency: string
  status: AccountStatus
  frozen_reason: string | null
  created_at: string
}

export interface Wallet {
  account_id: string
  real_balance: string
  bonus_balance: string
  total: string
  updated_at: string
}

export interface Transaction {
  id: string
  account_id: string
  type: TxType
  balance_type: 'real' | 'bonus'
  amount: string
  status: TxStatus
  reference: string | null
  metadata: Record<string, unknown> | null
  created_at: string
}

export interface TradeSymbol {
  id: string
  code: string
  display_name: string
  category: SymbolCategory
  pip_size: number
  is_active: boolean
  /** Latest price, present when the feed has seen a tick for this symbol. */
  price?: string
  epoch?: number
}

export interface Trade {
  id: string
  account_id: string
  symbol_id: string
  direction: Direction
  entry_price: string
  exit_price: string | null
  stake: string
  stop_loss: string | null
  take_profit: string | null
  status: TradeStatus
  pnl: string | null
  opened_at: string
  closed_at: string | null
  // Denormalised by the API so the UI never joins client-side.
  symbol_code: string
  symbol_name: string
  pip_size: number
}

export interface Signal {
  id: string
  symbol_id: string
  direction: Direction
  confidence: string
  entry_price: string
  stop_loss: string | null
  take_profit: string | null
  strategy: string
  created_at: string
  symbol_code: string
  symbol_name: string
  pip_size: number
}

export interface Tick {
  symbol: string
  price: string
  epoch: number
}

/**
 * What an auth call returns. There is no refresh token: it is delivered as an
 * httpOnly cookie the browser holds and sends on its own.
 */
export interface AccessGrant {
  access_token: string
  expires_in: number
}

export interface AuthResult extends AccessGrant {
  user: User
  /** Present only on register: the demo account created at signup. */
  account?: Account
}

export interface DepositResponse {
  transaction_id: string
  checkout_request_id: string
  amount_kes: string
  amount_usd: string
  status: TxStatus
}

export interface TradeSummary {
  open_pnl: string
  realised_today: string
  open_positions: number
  /** False when an open position's symbol has no live price: show it as stale. */
  prices_complete: boolean
}

export interface Page<T> {
  items: T[]
  total: number
  limit: number
  offset: number
  page: number
  pages: number
}

export interface List<T> {
  items: T[]
}
