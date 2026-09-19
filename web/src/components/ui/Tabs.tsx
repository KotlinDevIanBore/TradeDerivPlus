import * as RadixTabs from '@radix-ui/react-tabs'
import { cn } from '@/lib/cn'

export const Tabs = RadixTabs.Root

export function TabsList({ className, ...props }: RadixTabs.TabsListProps) {
  return (
    <RadixTabs.List
      className={cn('flex border-b border-hairline', className)}
      {...props}
    />
  )
}

export function TabsTrigger({ className, ...props }: RadixTabs.TabsTriggerProps) {
  return (
    <RadixTabs.Trigger
      className={cn(
        'relative -mb-px border-b-2 border-transparent px-4 py-3 text-body font-medium text-muted',
        'transition-colors hover:text-text',
        'data-[state=active]:border-accent data-[state=active]:text-text',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg',
        className,
      )}
      {...props}
    />
  )
}

export function TabsContent({ className, ...props }: RadixTabs.TabsContentProps) {
  return <RadixTabs.Content className={cn('focus:outline-none', className)} {...props} />
}
