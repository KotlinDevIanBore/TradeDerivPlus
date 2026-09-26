import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/Skeleton'
import { cn } from '@/lib/cn'
import { dayLabel, money, signedMoney, signedPercent } from '@/lib/format'
import { AdminPageHeader } from '../AdminShell'
import { adminApi, adminKeys } from '../adminApi'
import { DataTable, type Column } from '../components/DataTable'
import { FilterChips } from '../components/SearchInput'
import { Tile } from '../components/Tile'
import type { EdgeCheck, FinanceDayRow, FinanceRange, FinanceTypeRow } from '../adminTypes'

/**
 * What the house made, and whether it matches what the payout table says it
 * should. Every figure is house-side: positive means the house is ahead.
 */

/** The scope, stated on the screen rather than implied. CFD trades are a second
 *  source with the same shape and are deliberately not included. */
export const FINANCE_SCOPE_NOTE = 'Digit contracts only. CFD trades are not included.'

const RANGES: { label: string; value: FinanceRange }[] = [
  { label: '24 hours', value: '24h' },
  { label: '7 days', value: '7d' },
  { label: '30 days', value: '30d' },
  { label: 'All time', value: 'all' },
]

export function rangeLabel(range: FinanceRange): string {
  return RANGES.find((r) => r.value === range)?.label ?? '7 days'
}

/** A fraction to a signed percentage: 0.024 becomes +2.40%. */
export function edgePercent(value: string | number | null | undefined): string {
  if (value === null || value === undefined || value === '') return '—'
  return signedPercent(Number(value) * 100, 2)
}

/**
 * The standard error of a realised edge, in percentage points.
 *
 * A contract's outcome has a standard deviation of roughly one stake, so the
 * error on the edge is about 1/sqrt(n). Shown beside every edge figure because
 * the gap between realised and theoretical is meaningless until it exceeds this.
 */
export function edgeNoisePP(n: number): string {
  if (!n || n <= 0) return '—'
  return `±${(100 / Math.sqrt(n)).toFixed(1)} pp`
}

/**
 * The edge in one line.
 *
 * Below the sample threshold it withholds the comparison entirely rather than
 * printing two numbers and trusting the reader not to subtract them — that is
 * the presentation risk the investigation rated most likely to cause a wrong
 * decision.
 */
export function edgeReadout(edge: EdgeCheck): string {
  if (edge.too_few_to_read) {
    return `Too few contracts to read — ${edge.sample_size} of ${edge.min_sample}`
  }
  return `realised ${edgePercent(edge.realised)} vs theoretical ${edgePercent(edge.theoretical)}`
}

const TYPE_LABELS: Record<string, string> = {
  even_odd: 'Even / Odd',
  matches_differs: 'Matches / Differs',
  over_under: 'Over / Under',
}

export function contractTypeLabel(type: string): string {
  return TYPE_LABELS[type] ?? type
}

/**
 * House P&L by day, around a centre line.
 *
 * The same flex-column-per-bucket shape as the dashboard's deposit chart, with a
 * baseline in the middle so a losing day reads as a losing day. No charting
 * library: this is bars, and the admin bundle carries no chart dependency.
 */
function HousePnLChart({ rows }: { rows: FinanceDayRow[] }) {
  const peak = Math.max(1, ...rows.map((r) => Math.abs(Number(r.real_house_pnl))))

  return (
    <div className="rounded-card border border-border bg-surface px-4 py-3">
      <div className="flex items-baseline justify-between">
        <h2 className="text-card font-semibold">House P&amp;L by day, real money</h2>
        <span className="flex items-center gap-3 text-meta text-muted">
          <span className="flex items-center gap-1">
            <span className="h-2 w-2 rounded-sm bg-profit" /> house ahead
          </span>
          <span className="flex items-center gap-1">
            <span className="h-2 w-2 rounded-sm bg-accent" /> house behind
          </span>
        </span>
      </div>

      <div className="mt-4 flex h-[140px] items-stretch gap-1">
        {rows.map((r) => {
          const value = Number(r.real_house_pnl)
          const height = `${(Math.abs(value) / peak) * 100}%`
          return (
            <div
              key={r.day}
              className="flex flex-1 flex-col"
              title={`${dayLabel(r.day)} — ${signedMoney(r.real_house_pnl)} from ${r.contracts} contract${r.contracts === 1 ? '' : 's'}`}
            >
              <div className="flex flex-1 flex-col justify-end">
                {value > 0 ? <div className="rounded-t-sm bg-profit" style={{ height }} /> : null}
              </div>
              <div className="h-px bg-border" />
              <div className="flex flex-1 flex-col justify-start">
                {value < 0 ? <div className="rounded-b-sm bg-accent" style={{ height }} /> : null}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

const typeColumns: Column<FinanceTypeRow>[] = [
  {
    key: 'type',
    header: 'Contract type',
    render: (r) => <span className="font-medium text-text">{contractTypeLabel(r.contract_type)}</span>,
  },
  { key: 'contracts', header: 'Contracts', align: 'right', render: (r) => r.contracts },
  {
    key: 'staked',
    header: 'Real staked',
    align: 'right',
    render: (r) => <span className="tnum">{money(r.real.stakes)}</span>,
  },
  {
    key: 'pnl',
    header: 'Real house P&L',
    align: 'right',
    render: (r) => (
      <span className={cn('tnum', Number(r.real.house_pnl) < 0 ? 'text-accent' : 'text-profit')}>
        {signedMoney(r.real.house_pnl)}
      </span>
    ),
  },
  {
    key: 'realised',
    header: 'Realised edge',
    align: 'right',
    render: (r) =>
      r.edge.too_few_to_read ? (
        <span className="text-faint">—</span>
      ) : (
        <span className="tnum">{edgePercent(r.edge.realised)}</span>
      ),
  },
  {
    key: 'theoretical',
    header: 'Theoretical',
    align: 'right',
    render: (r) => <span className="tnum text-muted">{edgePercent(r.edge.theoretical)}</span>,
  },
  {
    key: 'n',
    header: 'n',
    align: 'right',
    render: (r) => (
      <span className={cn('tnum', r.edge.too_few_to_read && 'text-faint')}>{r.edge.sample_size}</span>
    ),
  },
]

const dayColumns: Column<FinanceDayRow>[] = [
  { key: 'day', header: 'Day', render: (r) => dayLabel(r.day) },
  { key: 'contracts', header: 'Contracts', align: 'right', render: (r) => r.contracts },
  {
    key: 'staked',
    header: 'Real staked',
    align: 'right',
    render: (r) => <span className="tnum">{money(r.real_stakes)}</span>,
  },
  {
    key: 'pnl',
    header: 'Real house P&L',
    align: 'right',
    render: (r) => <span className="tnum">{signedMoney(r.real_house_pnl)}</span>,
  },
  {
    key: 'promo',
    header: 'Promotional',
    align: 'right',
    render: (r) => <span className="tnum text-muted">{signedMoney(r.promotional_house_pnl)}</span>,
  },
  {
    key: 'demo',
    header: 'Demo contracts',
    align: 'right',
    render: (r) => <span className="text-muted">{r.demo_contracts}</span>,
  },
]

export function AdminFinance() {
  const [params, setParams] = useSearchParams()
  const range = (params.get('range') as FinanceRange) ?? '7d'

  const query = useQuery({
    queryKey: adminKeys.finance(range),
    queryFn: () => adminApi.finance(range),
  })

  const setRange = (next: FinanceRange) => {
    const p = new URLSearchParams(params)
    p.set('range', next)
    setParams(p, { replace: true })
  }

  const d = query.data

  return (
    <div>
      <AdminPageHeader title="Finance" breadcrumb={FINANCE_SCOPE_NOTE} />

      <div className="mb-4">
        <FilterChips value={range} onChange={setRange} options={RANGES} />
      </div>

      {query.isError ? (
        <div className="rounded-card border border-border bg-surface">
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        </div>
      ) : null}

      {query.isLoading || !d ? (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-[76px] rounded-card" />
          ))}
        </div>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
            <Tile
              label={`House P&L · ${rangeLabel(range)}`}
              value={signedMoney(d.real.house_pnl)}
              sub={`${d.real.contracts} contracts, real money`}
            />
            <Tile
              label="Volume staked"
              value={money(d.real.stakes)}
              sub={`${d.real.wins}W / ${d.real.losses}L / ${d.real.voids}V`}
            />
            <Tile
              label="Promotional cost"
              value={signedMoney(d.promotional.house_pnl)}
              sub="bonus-funded stakes on real accounts"
            />
            <Tile
              label="Edge"
              value={d.edge.too_few_to_read ? '—' : edgePercent(d.edge.realised)}
              sub={
                d.edge.too_few_to_read
                  ? `${d.edge.sample_size} of ${d.edge.min_sample} contracts`
                  : `${edgeNoisePP(d.edge.sample_size)} at n=${d.edge.sample_size}`
              }
            />
            {/* Net float is customer money held, never described as profit. */}
            <Tile
              label="Net float"
              value={signedMoney(d.flows.net_float_usd)}
              sub={`against ${money(d.flows.customer_liability_usd)} owed to customers`}
            />
          </div>

          <p className="mt-2 text-meta text-muted">
            {edgeReadout(d.edge)} · {d.demo_contracts} demo contracts, excluded from every money
            figure
            {d.edge.unpriced > 0 ? ` · ${d.edge.unpriced} contracts could not be priced` : ''}
          </p>

          <div className="mt-4">
            <HousePnLChart rows={d.by_day} />
          </div>

          <h2 className="mb-2 mt-5 text-card font-semibold">By contract type</h2>
          <DataTable
            columns={typeColumns}
            rows={d.by_type}
            rowKey={(r) => r.contract_type}
            empty={<p className="text-body text-muted">No settled contracts in this range.</p>}
          />

          <h2 className="mb-2 mt-5 text-card font-semibold">By day</h2>
          {d.by_day_capped ? (
            <p className="mb-2 text-meta text-muted">
              Showing the most recent {d.by_day.length} days of a longer range.
            </p>
          ) : null}
          <DataTable
            columns={dayColumns}
            rows={d.by_day}
            rowKey={(r) => r.day}
            empty={<p className="text-body text-muted">Nothing settled in this range.</p>}
          />
        </>
      )}
    </div>
  )
}
