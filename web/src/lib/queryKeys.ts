/**
 * Every React Query key in one place. Keys are account-scoped wherever the data
 * is, so switching account invalidates exactly the right set and nothing else.
 */
export const queryKeys = {
  me: () => ['me'] as const,
  accounts: () => ['accounts'] as const,
  wallet: (accountId: string) => ['wallet', accountId] as const,
  transactions: (accountId: string, filters?: unknown) =>
    filters === undefined
      ? (['transactions', accountId] as const)
      : (['transactions', accountId, filters] as const),
  trades: (accountId: string, status?: string) =>
    status === undefined
      ? (['trades', accountId] as const)
      : (['trades', accountId, status] as const),
  tradeSummary: (accountId: string) => ['tradeSummary', accountId] as const,
  digitContracts: (accountId: string, status?: string) =>
    status === undefined
      ? (['digitContracts', accountId] as const)
      : (['digitContracts', accountId, status] as const),
  symbols: () => ['symbols'] as const,
  signals: (symbol?: string) => (symbol ? (['signals', symbol] as const) : (['signals'] as const)),
  rate: () => ['rate'] as const,
}
