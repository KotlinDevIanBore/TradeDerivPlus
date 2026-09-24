import { describe, expect, it } from 'vitest'
import { ENABLE_CONSEQUENCE, killConsequence } from './Dashboard'

/**
 * The kill switch's dialog copy. The button's wiring is covered end to end;
 * what is checked here is the sentence, because it is the only part with
 * decisions in it and it is what an operator reads before stopping every run on
 * the platform.
 */
describe('killConsequence', () => {
  it('always says the feature is turned off too', () => {
    // The half operators are most likely to miss: stopping the runs alone would
    // leave users free to start again immediately.
    for (const n of [0, 1, 7]) {
      expect(killConsequence(n)).toMatch(/Automated trading will also be turned off/)
    }
  })

  it('does not claim to stop runs when none are live', () => {
    expect(killConsequence(0)).toMatch(/No runs are live right now/)
    expect(killConsequence(0)).not.toMatch(/will stop/)
  })

  it('counts one run in the singular', () => {
    expect(killConsequence(1)).toMatch(/1 live run will stop/)
  })

  it('counts several in the plural', () => {
    expect(killConsequence(7)).toMatch(/7 live runs will stop/)
  })

  // A run with a contract in flight does not end the instant the switch is
  // thrown, and the dialog says so rather than promising an immediacy the
  // server does not offer.
  it('says a live run stops after its contract settles, not at once', () => {
    expect(killConsequence(2)).toMatch(/after the contract each has in flight settles/)
  })
})

/**
 * The way back. The claim worth checking is the one an operator is most likely
 * to assume the opposite of.
 */
describe('ENABLE_CONSEQUENCE', () => {
  it('says what is restored', () => {
    expect(ENABLE_CONSEQUENCE).toMatch(/able to start auto runs again/)
  })

  // Runs never resume — not across a service restart and not across the kill
  // switch. An operator who thought otherwise would turn the feature on
  // expecting the stopped runs to pick up where they left off.
  it('says nothing resumes', () => {
    expect(ENABLE_CONSEQUENCE).toMatch(/stay stopped/)
    expect(ENABLE_CONSEQUENCE).toMatch(/nothing resumes/)
  })
})
