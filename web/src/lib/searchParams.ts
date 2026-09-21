/**
 * Builds an updater for `setSearchParams` that merges into the query string
 * instead of replacing it.
 *
 * Passing an object to `setSearchParams` discards every parameter it does not
 * mention. That is harmless while each screen owns its own URL, and becomes a
 * bug the moment two independent things share one -- a chart instrument and an
 * open panel, say, where changing either would silently reset the other.
 *
 * A `null` value removes the parameter, for the cases where dropping one
 * alongside setting another is the point.
 */
export function mergeParams(updates: Record<string, string | null>) {
  return (previous: URLSearchParams) => {
    const next = new URLSearchParams(previous)

    for (const [key, value] of Object.entries(updates)) {
      if (value === null) next.delete(key)
      else next.set(key, value)
    }

    return next
  }
}
