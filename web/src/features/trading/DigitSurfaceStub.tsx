/**
 * Placeholder main surface while digit contracts are flagged on.
 *
 * Phase 0 only: proves the flag reaches the frontend and swaps `/`. The real
 * digit ticket, digit row and settlement UI are Phase 2 — not this file.
 */
export function DigitSurfaceStub() {
  return (
    <div className="flex min-h-[50vh] flex-col items-center justify-center gap-3 px-gutter py-16 text-center">
      <p className="text-title font-bold tracking-tight">Digit contracts coming soon</p>
      <p className="max-w-md text-body text-muted">
        This surface is on because <code className="text-meta">digit_contracts_enabled</code> is
        true. The proportional trading screen is still in the codebase and returns when the flag is
        off — flip the database row, wait a few seconds, and reload.
      </p>
    </div>
  )
}
