# Why the digit payout table is marked "not final for real-money use"

**Date:** 26 September 2026 · **Kind:** investigation, then a fix.
**Status:** **§5's inconsistency is RESOLVED — see §9.** Everything above §9 is kept as
the record of what was wrong and how it got that way. The numbers quoted in §1, §2
and §5 are the *old* ones.
**Subject:** `internal/models/digit_payout.go` — **not** `internal/service/digit_payout.go`, which does not exist. The table lives in `models`, beside the types it prices.

---

## 0. Answer first

**It is a disclosure and sign-off problem, not a maths error — but it is not only
that.** The numbers compute correctly, the code's own documentation of them is
accurate, and the expected returns agree with every figure published elsewhere in
this project. Nobody with authority ever chose them.

**However, the investigation turned up a concrete inconsistency nobody has
recorded**: two pairs of contracts with *identical* win probability are priced
differently, because Matches and Differs are round numbers while Over/Under is
computed from a formula. That is a real product defect sitting inside the
"provisional" label, and it is the thing a product sign-off would actually have
to resolve. See §5.

---

## 1. The comment, verbatim

`internal/models/digit_payout.go:9-14`:

```go
// Placeholder payout ratios for v1. NOT final for real-money use — replace
// once product confirms the table. Even/Odd match the benchmark screenshot
// (95.20% → $10 stake wins $19.52 total credit). Match/Differ/Over/Under are
// illustrative placeholders consistent with the investigation §2.3 sketch.
//
// payout_ratio means: win credit = stake × (1 + ratio). Loss forfeits stake.
```

And the three stored values, `digit_payout.go:16-20`:

```go
var (
	placeholderEvenOdd = decimal.RequireFromString("0.9520") // PLACEHOLDER (benchmark)
	placeholderMatch   = decimal.RequireFromString("8.0000") // PLACEHOLDER ~9x-ish stake return as credit
	placeholderDiffer  = decimal.RequireFromString("0.1000") // PLACEHOLDER
)
```

Over/Under is not a stored constant. It is computed at `digit_payout.go:65-67`:

```go
// Fair profit ≈ (10/winCount) - 1; apply 0.95 house factor.
fair := decimal.NewFromInt(10).Div(decimal.NewFromInt(int64(winCount))).Sub(decimal.NewFromInt(1))
return fair.Mul(decimal.RequireFromString("0.95")).Round(4), nil
```

---

## 2. The table as it actually prices

Computed by calling `PayoutRatioFor`, `WinProbabilityFor` and `ExpectedReturnFor`
directly, not read off a document.

| Contract | Win prob | Ratio | Expected return |
|---|---:|---:|---:|
| Over 0 / Under 9 | 0.9 | 0.1056 | **−0.50%** ← least bad |
| Differs (any digit) | 0.9 | 0.1000 | −1.00% |
| Over 1 / Under 8 | 0.8 | 0.2375 | −1.00% |
| Over 2 / Under 7 | 0.7 | 0.4071 | −1.50% |
| Over 3 / Under 6 | 0.6 | 0.6333 | −2.00% |
| **Even / Odd** | 0.5 | 0.9520 | **−2.40%** |
| Over 4 / Under 5 | 0.5 | 0.9500 | −2.50% |
| Over 5 / Under 4 | 0.4 | 1.4250 | −3.00% |
| Over 6 / Under 3 | 0.3 | 2.2167 | −3.50% |
| Over 7 / Under 2 | 0.2 | 3.8000 | −4.00% |
| Over 8 / Under 1 | 0.1 | 8.5500 | −4.50% |
| **Matches (any digit)** | 0.1 | 8.0000 | **−10.00%** ← worst |

Win probabilities are a property of the selection alone, on the stated assumption
that last digits are uniform and independent — verified on this feed
(`auto-bot-investigation.md` §2.5), and asserted by
`digit_payout_test.go:13-60`.

---

## 3. Origin of the comment

| Question | Answer |
|---|---|
| Which commit | `6d7d68f`, **23 September 2026** — `feat(api): digit_contracts schema, lastDigit, and payout placeholders` |
| Author | `KotlinDevIanBore`, **co-authored-by `Cursor <cursoragent@cursor.com>`** |
| Human decision or scaffolding? | **Scaffolding.** It was written in the same commit that first created the table, by an AI-assisted pairing, as part of standing up digit contracts |
| Predates or postdates the auto-bot work? | **Predates it by a day.** The auto-bot investigation and plan landed in `50b2a41` on 24 September |

The commit message says the same thing the comment does, so the intent was
explicit rather than accidental:

> Even/Odd uses the benchmark 95.20%; Match/Differ/Over/Under ratios are explicit
> placeholders pending product confirmation.

**One correction to the brief's framing.** The comment cites "the investigation
§2.3 sketch". That is **not** `auto-bot-investigation.md` §2.3, which is about
chart history on first render and says nothing about payouts. It is
`digit-contracts-investigation.md` §2.3, "Payout approach" — which did exist when
the comment was written. The citation is correct but under-specified, and reading
it against the wrong document leads nowhere.

`50b2a41` (24 September) later **corrected** part of this file rather than
changing any number: lines 51-55 and 103-106 were rewritten when the auto-bot
investigation found that an earlier comment claiming a flat "~5% house edge" was
wrong (§4). The ratios themselves have never been touched since the day they were
written.

---

## 4. What the rest of the project says is provisional about them

Three documents, consistent with each other.

**`digit-contracts-investigation.md` §2.3** — the source the comment points at.
It recommends a static table for v1 and is explicit that the numbers are not
engineering's to choose:

> **Illustration from the benchmark (not a commitment):** $10 stake → $19.52 shown
> with 95.20% … **Client must confirm** win credit formula and the exact
> percentages per type (section 9).

> Engineering can ship a config struct / DB seed; **product owns the numbers**.

Its §9 "Open questions for the client" opens with *"These are product decisions;
engineering should not invent them silently"*, and item 1 asks product to provide
ratios for every type.

**`auto-bot-investigation.md` §3.1** — computes the full table independently and
publishes it. Its figures match §2 above to the last digit. It also records that
the code once claimed a flat 5% edge and that this was false: the 0.95 factor
applies to the *profit*, so the realised edge is `5% × (1 − p)` and varies from
0.50% to 4.50% by barrier. That correction is now in the code at
`digit_payout.go:51-55`.

**`auto-bot-plan.md`** — ties the flag to the table twice. At `:29-32`:

> Until product replaces the payout table (`internal/models/digit_payout.go:9-14`),
> that flag should stay off.

And §10's first open question:

> **Should a bot ship at all against a payout table marked "NOT final for
> real-money use"** (`digit_payout.go:9-14`)?

**Nothing anywhere records product having answered.** `bot_real_money_enabled`
has been `false` since it was introduced and is still `false`.

### Where each number actually came from

| Family | Provenance | Character |
|---|---|---|
| Even/Odd `0.9520` | Read off a **benchmark screenshot** of a competitor's product | A real market number, copied — the only one with any external basis |
| Over/Under | **Computed**: fair odds `(10/w) − 1`, times a 0.95 house factor on profit | Principled and internally consistent; not a placeholder in the usual sense despite the `PLACEHOLDER` label at `:49` |
| Matches `8.0000` | A **round number**. The inline comment says "~9x-ish stake return as credit" | Invented |
| Differs `0.1000` | A **round number** | Invented |

---

## 5. The inconsistency nobody has written down

Because Over/Under is computed and Matches/Differs are round numbers, **the table
prices economically identical contracts differently**:

| Pair | Win prob | Ratio | Expected return |
|---|---:|---:|---:|
| **Matches** (any digit) | 0.1 | 8.0000 | **−10.00%** |
| **Over 8 / Under 1** | 0.1 | 8.5500 | **−4.50%** |
| **Differs** (any digit) | 0.9 | 0.1000 | **−1.00%** |
| **Over 0 / Under 9** | 0.9 | 0.1056 | **−0.50%** |

Matches and Over 8 are the same bet — one digit in ten — and Matches costs the
customer **2.2× more**. Differs and Under 9 are the same bet — nine digits in ten
— and Differs costs **2× more**.

Applying the table's own 0.95-on-profit rule to Matches would give `9 × 0.95 =
8.55` and an edge of −4.50%, identical to Over 8. Nothing in the code or the
documents explains why Matches departs from the rule the rest of the table
follows.

This is not a bug in the sense of producing a wrong answer — the code prices
exactly what the table says. It is the substance behind the word "provisional",
and the concrete question a product sign-off has to answer: **is Matches
deliberately twice as expensive as the identical Over 8 contract, or is 8.0000
simply a round number nobody revisited?**

Currently the product cannot even offer the comparison: the UI shows Matches with
an amber warning at −10%, but nothing tells a customer that Over 8 is the same bet
at less than half the cost.

---

## 6. Do the numbers disagree with anything documented?

**No.** This was checked in both directions:

- Every figure in `auto-bot-investigation.md` §3.1's table matches the computed
  values exactly.
- `digit_payout_test.go:17-37` asserts eleven of them against the pricing code, so
  the published numbers and the shipped code cannot drift apart silently. Its
  header says so: *"If someone replaces the placeholder payout table, these
  numbers change and this test is where that shows up — which is the point: the UI
  quotes these figures to users."*
- `auto-bot-plan.md`'s "−0.50% to −10.00%" bounds match the computed extremes.
- The one historical disagreement — a code comment claiming a flat ~5% edge — was
  found by the auto-bot investigation and corrected in `50b2a41`. It is gone.

The only rounding subtlety is recorded in the test itself: Over 3 / Under 6 has
six winning digits, making the fair profit a recurring decimal, so the stored
4-decimal ratio yields −2.002% rather than the −2.00% the document shows at two
decimal places. The test asserts the true figure and explains why.

---

## 7. So: maths, or disclosure?

**Primarily disclosure and sign-off.**

- The arithmetic is right. Probabilities are correct for uniform independent
  digits, a stated and separately verified assumption.
- The expected return is computed, surfaced on the quote endpoint, and shown in
  the UI next to the Start button, so the customer is told the cost rather than
  being left to infer it.
- The code's own documentation of its behaviour is accurate, including a
  correction made when it was found not to be.
- What is missing is a person with authority saying "these are our numbers".
  Engineering chose two of them (Matches, Differs) by picking round figures, and
  the third (Even/Odd) by copying a competitor's screenshot. Neither is a basis
  for taking real money.

**With one genuine substantive issue inside it**, which is §5: the table is not
internally consistent, and two contract families are priced roughly twice as
harshly as identical bets elsewhere in the same table. That is the kind of thing
that is a disclosure problem right up until a customer notices, and then becomes a
fairness problem.

**Not unclear.** The evidence is unusually complete: the comment says what it is,
the commit message agrees, the investigation that specified the approach says
"product owns the numbers", the plan ties the flag to it explicitly, and the test
pins the resulting figures.

---

## 8. What would close this

Not engineering work, except for the last line.

1. **Product supplies a table**, or confirms this one. `digit-contracts-investigation.md`
   §9 item 1 is the list of what is needed: the win-credit formula and a ratio per
   type, per barrier where relevant, and whether they are identical on all symbols.
2. **Resolve §5** — either bring Matches and Differs onto the same 0.95 rule as
   Over/Under, or record why they differ.
3. **Decide the demo question** (§9 item 9): same table on demo as real, or a more
   generous demo table.
4. **Then** `bot_real_money_enabled` can be considered — and that is a separate
   decision again, with its own open questions in `auto-bot-plan.md` §10.

Replacing the numbers is a one-line change each, plus updating
`digit_payout_test.go`'s expected figures — which is exactly what that test exists
to force.

---

## 9. Resolved: one rule for the whole table

**Changed 26 September 2026**, at the project owner's direction: apply the
0.95-on-profit rule already used for Over/Under to Matches and Differs, and leave
Even/Odd alone.

### What changed in the code

`internal/models/digit_payout.go`. The rule was extracted from inside the
Over/Under branch into `houseRatio(winCount)` and is now the single definition
used by all three families:

```go
func houseRatio(winCount int) decimal.Decimal {
	fair := decimal.NewFromInt(10).Div(decimal.NewFromInt(int64(winCount))).Sub(decimal.NewFromInt(1))
	return fair.Mul(houseFactor).Round(4)
}
```

- `placeholderMatch = 8.0000` → `houseRatio(1)` = **8.5500**
- `placeholderDiffer = 0.1000` → `houseRatio(9)` = **0.1056**
- `placeholderEvenOdd` → renamed `benchmarkEvenOdd`, value **unchanged at 0.9520**

Even/Odd is kept because it is the one figure with an external basis — read off a
benchmark screenshot, where $10 at 95.20% returns $19.52 — rather than a number
this codebase invented. It sits 0.0020 above what the rule would give for a
five-in-ten selection, and that difference is deliberate and documented in the
file.

### The table now

| Contract | Win prob | Ratio | Expected return | Change |
|---|---:|---:|---:|---|
| Over 0 / Under 9 | 0.9 | 0.1056 | **−0.50%** | — |
| **Differs (any digit)** | 0.9 | **0.1056** | **−0.50%** | was −1.00% |
| Over 1 / Under 8 | 0.8 | 0.2375 | −1.00% | — |
| Over 2 / Under 7 | 0.7 | 0.4071 | −1.50% | — |
| Over 3 / Under 6 | 0.6 | 0.6333 | −2.00% | — |
| **Even / Odd** | 0.5 | 0.9520 | **−2.40%** | — (benchmark, deliberately kept) |
| Over 4 / Under 5 | 0.5 | 0.9500 | −2.50% | — |
| Over 5 / Under 4 | 0.4 | 1.4250 | −3.00% | — |
| Over 6 / Under 3 | 0.3 | 2.2167 | −3.50% | — |
| Over 7 / Under 2 | 0.2 | 3.8000 | −4.00% | — |
| Over 8 / Under 1 | 0.1 | 8.5500 | **−4.50%** | — |
| **Matches (any digit)** | 0.1 | **8.5500** | **−4.50%** | was −10.00% |

Every edge is now exactly `5% × (1 − p)` except Even/Odd, which is 0.10pp better
than the rule would give. The two pairs that were mispriced now agree:

- Matches and Over 8 / Under 1 — all one-digit-in-ten — all 8.5500, all −4.50%.
- Differs and Over 0 / Under 9 — all nine-digits-in-ten — all 0.1056, all −0.50%.

The range narrowed from −0.50%…−10.00% to **−0.50%…−4.50%**, so the worst contract
on the platform now costs less than half what it did.

### Tests

`digit_payout_test.go` gained two properties rather than two more remembered
numbers:

- `TestSelectionsWithTheSameOddsArePricedTheSame` groups every selection by win
  probability and requires one ratio per group. A hand-picked number reintroduced
  anywhere is caught wherever it is put, not only in the two places that were
  wrong.
- `TestEveryEdgeFollowsTheHouseRule` asserts the edge *is* `houseFactor × (1 − p)`
  for everything the rule prices, so the rule is stated as arithmetic rather than
  as a table someone has to keep in step.

The existing expected-return table was updated: Differs −1.00% → −0.50%, Matches
−10.00% → −4.50%.

### One consequence that would have gone unnoticed

`BotTicket.tsx` warned "This contract costs the most per play" below a threshold
of **−0.05**, chosen when Matches sat alone at −10% and the next worst was −4.5%.
With Matches now at −4.5%, **nothing would have crossed that line and the warning
would have silently stopped appearing.** The threshold moved to −0.042, which sits
in the gap between the new worst band (−4.50%) and the next (−4.00%), so it still
flags exactly the most expensive contracts.

### What this does *not* resolve

**The table is still not product-approved.** The file still says NOT FINAL FOR
REAL-MONEY USE, and `bot_real_money_enabled` should stay off. This change made the
numbers internally consistent; it did not make them anyone's decision. §8's list
stands, minus item 2.
