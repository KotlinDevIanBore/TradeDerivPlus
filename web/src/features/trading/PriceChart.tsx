import { useMemo } from 'react'
import {
  Area,
  AreaChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  YAxis,
} from 'recharts'
import { cn } from '@/lib/cn'
import { price as fmtPrice } from '@/lib/format'
import { dec } from '@/lib/money'
import type { Tick } from '@/types'

import type { ChartRange } from './chartRange'

/**
 * The tick chart. There is no candle endpoint, so this draws the tick series
 * directly; the range toggle slices the buffer rather than refetching.
 *
 * Stop loss and take profit are drawn as reference lines while the order ticket
 * has them set, so a user sizing a trade sees where the levels sit.
 */
export function PriceChart({
  ticks,
  pipSize,
  range,
  stopLoss,
  takeProfit,
  stale,
  className,
}: {
  ticks: Tick[]
  pipSize: number
  range: ChartRange
  stopLoss?: string | null
  takeProfit?: string | null
  stale?: boolean
  className?: string
}) {
  const data = useMemo(() => {
    const cutoff = Date.now() / 1000 - range * 60
    return ticks
      .filter((t) => t.epoch >= cutoff)
      .map((t) => ({ epoch: t.epoch, value: dec(t.price).toNumber() }))
  }, [ticks, range])

  const rising = useMemo(() => {
    if (data.length < 2) return true
    return data[data.length - 1].value >= data[0].value
  }, [data])

  const stroke = rising ? '#22C55E' : '#E24B4A'

  // Pad the domain so the line never touches the edges and the reference lines
  // stay visible when they sit just outside the traded range.
  const domain = useMemo<[number, number]>(() => {
    const values = data.map((d) => d.value)
    for (const level of [stopLoss, takeProfit]) {
      if (level) values.push(dec(level).toNumber())
    }
    if (values.length === 0) return [0, 1]

    const min = Math.min(...values)
    const max = Math.max(...values)
    const pad = (max - min || max * 0.001) * 0.15
    return [min - pad, max + pad]
  }, [data, stopLoss, takeProfit])

  if (data.length === 0) return null

  return (
    <div className={cn('h-[240px] w-full transition-opacity lg:h-[340px]', stale && 'opacity-40', className)}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 8 }}>
          <defs>
            <linearGradient id="priceFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={stroke} stopOpacity={0.22} />
              <stop offset="100%" stopColor={stroke} stopOpacity={0} />
            </linearGradient>
          </defs>

          <YAxis domain={domain} hide />

          <Tooltip
            contentStyle={{
              background: '#1A1A1D',
              border: '1px solid #232326',
              borderRadius: 12,
              fontSize: 12,
            }}
            labelFormatter={() => ''}
            formatter={(value) => [fmtPrice(String(value), pipSize), 'Price']}
            cursor={{ stroke: '#232326', strokeWidth: 1 }}
          />

          {stopLoss ? (
            <ReferenceLine
              y={dec(stopLoss).toNumber()}
              stroke="#E24B4A"
              strokeDasharray="4 4"
              strokeWidth={1}
              label={{ value: 'SL', position: 'insideLeft', fill: '#E24B4A', fontSize: 10 }}
            />
          ) : null}

          {takeProfit ? (
            <ReferenceLine
              y={dec(takeProfit).toNumber()}
              stroke="#22C55E"
              strokeDasharray="4 4"
              strokeWidth={1}
              label={{ value: 'TP', position: 'insideLeft', fill: '#22C55E', fontSize: 10 }}
            />
          ) : null}

          <Area
            type="linear"
            dataKey="value"
            stroke={stroke}
            strokeWidth={1.5}
            fill="url(#priceFill)"
            isAnimationActive={false}
            dot={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}
