/**
 * Every React Query key in one place. Keys are account-scoped wherever the data
 * is, so switching account invalidates exactly the right set and nothing else.
 */
export const queryKeys = {
  me: () => ['me'] as const,
  /** Verification status plus whether an upload was started and never finished. */
  kycState: () => ['kycState'] as const,
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
  digitQuote: (params: unknown) => ['digitQuote', params] as const,
  botRuns: (accountId: string, status?: string) =>
    status === undefined
      ? (['botRuns', accountId] as const)
      : (['botRuns', accountId, status] as const),
  // Keyed by run rather than by account: a run is the thing being watched, and
  // its id is stable while its account's other queries come and go.
  botRun: (runId: string) => ['botRun', runId] as const,
  symbols: () => ['symbols'] as const,
  signals: (symbol?: string) => (symbol ? (['signals', symbol] as const) : (['signals'] as const)),
  rate: () => ['rate'] as const,
}
