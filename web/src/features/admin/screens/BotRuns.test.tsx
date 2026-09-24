import { describe, expect, it } from 'vitest'
import { stopConsequence } from './BotRuns'

/**
 * The per-row stop's dialog copy. Same standard as the kill switch's: say
 * exactly what happens, and do not promise an immediacy the server does not
 * offer.
 */
describe('stopConsequence', () => {
  const run = { user_phone: '254712345678', account_number: '900002', status: 'running' as const }

  it('names whose run is being stopped', () => {
    // An operator acting on the wrong row is the failure this guards against,
    // so the confirmation repeats the user and the account rather than "this run".
    expect(stopConsequence(run)).toMatch(/254712345678/)
    expect(stopConsequence(run)).toMatch(/account 900002/)
  })

  it('says a contract in flight still has to settle', () => {
    expect(stopConsequence(run)).toMatch(/after any contract it has in flight settles/)
  })

  // The difference from the kill switch, and the one an operator reaching for
  // the wrong control most needs to see.
  it('says the feature stays on and they can start another', () => {
    expect(stopConsequence(run)).toMatch(/Automated trading stays on/)
    expect(stopConsequence(run)).toMatch(/start another run/)
  })

  it('does not offer to stop a run that is already stopping', () => {
    const text = stopConsequence({ ...run, status: 'stopping' })
    expect(text).toMatch(/already stopping/)
    expect(text).not.toMatch(/will stop after any contract/)
  })
})
