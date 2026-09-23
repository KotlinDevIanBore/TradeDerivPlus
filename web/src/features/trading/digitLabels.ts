import type { DigitContract, DigitContractType, DigitSelection } from '@/types'

export function contractTypeLabel(type: DigitContractType | string): string {
  switch (type) {
    case 'even_odd':
      return 'Even/Odd'
    case 'matches_differs':
      return 'Matches/Differs'
    case 'over_under':
      return 'Over/Under'
    default:
      return type
  }
}

export function selectionLabel(
  type: DigitContractType | string,
  selection: DigitSelection,
): string {
  switch (type) {
    case 'even_odd':
      return selection.parity === 'even' ? 'Even' : 'Odd'
    case 'matches_differs':
      return `${selection.mode === 'match' ? 'Match' : 'Differ'} ${selection.digit}`
    case 'over_under':
      return `${selection.side === 'over' ? 'Over' : 'Under'} ${selection.barrier}`
    default:
      return ''
  }
}

export function contractHeadline(c: Pick<DigitContract, 'contract_type' | 'selection' | 'symbol_code'>): string {
  return `${c.symbol_code} · ${selectionLabel(c.contract_type, c.selection)}`
}
