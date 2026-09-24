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

export interface DigitQuote {
  contract_type: DigitContractType
  selection: DigitSelection
  stake: string
  payout_ratio: string
  potential_payout: string
  /**
   * The economics of this selection, served by the API rather than derived here
   * so the figure shown to a user cannot drift from the payout table that prices
   * the contract. `expected_return` is profit per unit staked and is negative for
   * every selection on the current table.
   */
  win_probability: string
  expected_return: string
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
  /** The automated run that placed this, or null when a person did. */
  bot_run_id: string | null
  symbol_code: string
  symbol_name: string
  pip_size: number
}

export type BotRunStatus = 'running' | 'stopping' | 'stopped'

/**
 * Why a run ended. Split by origin because the UI treats them differently: what
 * the user asked for reads as success or as their own limit working, what the
 * platform imposed needs explaining, and a fault needs apologising for.
 */
export type BotStopReason =
  // Asked for.
  | 'user'
  | 'stop_loss'
  | 'take_profit'
  | 'max_consecutive_losses'
  | 'max_contracts'
  | 'max_duration'
  // Imposed.
  | 'admin_kill'
  | 'admin_stop'
  | 'disabled'
  | 'insufficient_funds'
  | 'account_frozen'
  | 'market_closed'
  // Faults.
  | 'feed_down'
  | 'restart'
  | 'shutdown'
  | 'error'

/**
 * The stop conditions a run was started with. Only stop_loss is required — a run
 * with no floor is not something this product offers. Money is a string, as
 * everywhere.
 */
export interface BotLimits {
  stop_loss: string
  take_profit?: string
  max_consecutive_losses?: number
  max_contracts?: number
  max_duration_s?: number
}

export interface BotRun {
  id: string
  account_id: string
  symbol_id: string
  status: BotRunStatus
  stop_reason: BotStopReason | null
  last_error: string | null
  contract_type: DigitContractType
  selection: DigitSelection
  stake: string
  interval_ms: number
  limits: BotLimits
  /** Real + bonus when the run began. Informational: stop-loss is judged on realised_pnl. */
  starting_balance: string
  contracts_placed: number
  wins: number
  losses: number
  voids: number
  consecutive_losses: number
  consecutive_voids: number
  realised_pnl: string
  started_at: string
  stopped_at: string | null
  updated_at: string
  symbol_code: string
  symbol_name: string
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
  /**
   * Whether this user may start an automated run. Already narrowed by the
   * allowlist server-side, so it answers "show the control" directly rather than
   * needing to be combined with anything else.
   */
  bot_enabled?: boolean
  /**
   * Whether runs are allowed on a real account. False for now, which is what
   * keeps automated trading demo-only; the ticket reads it to explain itself up
   * front instead of failing at submit.
   */
  bot_real_money_enabled?: boolean
  /**
   * The server's per-contract stake ceiling, as a string. Sent so a ticket can
   * state the limit and refuse before a round trip; the server enforces it either
   * way, and this is the one copy that cannot drift from it.
   */
  digit_max_stake?: string
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
