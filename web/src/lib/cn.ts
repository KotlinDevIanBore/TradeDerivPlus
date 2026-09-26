import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

/**
 * Merge Tailwind classes, with later conflicting utilities winning.
 *
 * Extended, not the stock twMerge, because the stock one only knows Tailwind's
 * own font sizes (text-xs … text-9xl). It filed this project's `text-meta`,
 * `text-body` and friends under text COLOUR, so `cn('text-meta font-semibold',
 * 'text-muted')` dropped the size and the element fell back to the reset's
 * `font-size: 100%` -- 16px where the source said 12. Thirty call sites rendered
 * at the wrong size that way, the contract-type tab row among them, which is how
 * "Over/Under" ended up 28px past the edge of a 375px phone. See
 * docs/investigation-reports/mobile-trading-screen-layout.md §1.
 *
 * The list below must match `theme.extend.fontSize` in tailwind.config.js;
 * cn.test.ts pins both the behaviour and the list.
 */
export const CUSTOM_FONT_SIZES = ['nano', 'meta', 'body', 'card', 'section', 'title', 'hero'] as const

const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      'font-size': [{ text: [...CUSTOM_FONT_SIZES] }],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
