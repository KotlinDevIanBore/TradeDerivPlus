import { forwardRef } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { Slot } from '@radix-ui/react-slot'
import { Loader2 } from 'lucide-react'
import { cn } from '@/lib/cn'

const button = cva(
  'inline-flex items-center justify-center gap-2 rounded-xl font-semibold transition-colors ' +
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 ' +
    'focus-visible:ring-offset-bg disabled:pointer-events-none disabled:opacity-40 select-none',
  {
    variants: {
      variant: {
        primary: 'bg-accent text-white hover:bg-accent-hover active:bg-accent-hover',
        outline: 'border border-border bg-transparent text-text hover:bg-elevated',
        ghost: 'bg-transparent text-muted hover:bg-elevated hover:text-text',
        buy: 'bg-profit text-black hover:brightness-110',
        sell: 'bg-accent text-white hover:bg-accent-hover',
        danger: 'border border-accent/50 bg-transparent text-accent hover:bg-accent-subtle',
      },
      size: {
        // 44px minimum so every control clears the tap-target guideline.
        sm: 'h-9 px-3 text-meta',
        md: 'h-11 px-4 text-body',
        lg: 'h-[52px] px-5 text-card',
      },
      block: { true: 'w-full', false: '' },
    },
    defaultVariants: { variant: 'primary', size: 'md', block: false },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof button> {
  asChild?: boolean
  loading?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { className, variant, size, block, asChild, loading, children, disabled, ...props },
  ref,
) {
  const classes = cn(button({ variant, size, block }), className)

  // With asChild the caller supplies the element (usually a Link), and Slot
  // can only merge onto a single child -- so no spinner is added here.
  // A link never has a pending state anyway.
  if (asChild) {
    return (
      <Slot ref={ref} className={classes} {...props}>
        {children}
      </Slot>
    )
  }

  return (
    <button
      ref={ref}
      className={classes}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? (
        <>
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          <span className="sr-only">Working</span>
        </>
      ) : null}
      {children}
    </button>
  )
})
