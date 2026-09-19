import { TrendingUp } from 'lucide-react'

/**
 * Login and register share this frame: one centred column, no card border, the
 * page background carrying the form. Red appears only on the primary button.
 */
export function AuthLayout({
  title,
  subtitle,
  children,
  footer,
}: {
  title: string
  subtitle: string
  children: React.ReactNode
  footer: React.ReactNode
}) {
  return (
    <div className="flex min-h-full flex-col justify-center px-gutter py-10">
      <div className="mx-auto w-full max-w-[400px]">
        <div className="mb-8">
          <div className="mb-5 flex items-center gap-2">
            <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-accent">
              <TrendingUp className="h-5 w-5 text-white" aria-hidden />
            </span>
            <span className="text-card font-bold tracking-tight">TradeDerivPlus</span>
          </div>
          <h1 className="text-title font-bold tracking-tight">{title}</h1>
          <p className="mt-1 text-body text-muted">{subtitle}</p>
        </div>

        {children}

        <div className="mt-6 text-center text-body text-muted">{footer}</div>
      </div>
    </div>
  )
}
