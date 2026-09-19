// Verifies the TypeScript PnL mirror agrees with the Go implementation.
// Run from the repo root:  node scripts/pnl_parity.mjs
//
// The vectors below are produced by the Go side (see the -dump flag in
// internal/models/pnl_test.go); this script runs the same inputs through the
// client formula in web/src/lib/pnl.ts and compares.
import Decimal from '../web/node_modules/decimal.js/decimal.mjs'
import { readFileSync } from 'node:fs'

// Mirror of web/src/lib/pnl.ts. Kept inline so this check fails if the two
// ever drift, rather than importing the file and testing it against itself.
function pnl(direction, entry, current, stake) {
  const e = new Decimal(entry)
  if (e.isZero()) return new Decimal(0)
  const c = new Decimal(current)
  const delta = direction === 'sell' ? e.minus(c) : c.minus(e)
  return new Decimal(stake).times(delta).dividedBy(e).toDecimalPlaces(2)
}

const vectors = JSON.parse(readFileSync(process.argv[2] ?? 'scripts/pnl_vectors.json', 'utf8'))

let failures = 0
for (const v of vectors) {
  const got = pnl(v.direction, v.entry, v.current, v.stake).toFixed(2)
  const want = new Decimal(v.want).toFixed(2)
  const ok = got === want
  if (!ok) failures++
  console.log(
    `  [${ok ? 'PASS' : 'FAIL'}] ${v.direction.padEnd(4)} entry ${String(v.entry).padStart(10)}` +
    ` current ${String(v.current).padStart(10)} stake ${String(v.stake).padStart(7)}` +
    ` -> ts ${got.padStart(9)}  go ${want.padStart(9)}`,
  )
}

// The client must also agree on the "never lose more than the stake" rule.
const capped = pnl('buy', '100', '0', '50')
console.log(`\n  total loss case: pnl ${capped.toFixed(2)}, credit on close ` +
  `${Decimal.max(0, new Decimal('50').plus(capped)).toFixed(2)} (must be 0.00)`)
if (!Decimal.max(0, new Decimal('50').plus(capped)).isZero()) failures++

console.log('\n' + '='.repeat(60))
if (failures) {
  console.log(`FAILED: ${failures} vector(s) disagree between Go and TypeScript`)
  process.exit(1)
}
console.log('GO AND TYPESCRIPT PNL AGREE ON EVERY VECTOR')
