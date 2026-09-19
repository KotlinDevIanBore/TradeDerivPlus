export function PageHeader({
  title,
  action,
}: {
  title: string
  action?: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-3 px-gutter pb-2 pt-5 lg:px-6">
      <h1 className="text-title font-bold tracking-tight">{title}</h1>
      {action}
    </div>
  )
}
