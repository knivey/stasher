import type { Location } from '../api/types'

export interface LocationOption { id: number; name: string; depth: number }
export interface LocationRow { loc: Location; depth: number }

// Flattened tree rows for the locations management view: each root in name
// order, immediately followed by its descendants in name order (path-name
// order). The visited set makes a corrupt parent cycle unable to loop
// forever; cycle members are unreachable from any root, so they are appended
// at the end (still rendered exactly once).
export function locationTreeRows(locations: Location[]): LocationRow[] {
  const byId = new Map(locations.map(l => [l.id, l]))
  const childrenOf = new Map<number, Location[]>()
  const roots: Location[] = []
  for (const l of locations) {
    // A missing parent id is treated as a root, matching the depth walk below.
    if (l.parent_id === null || !byId.has(l.parent_id)) roots.push(l)
    else childrenOf.set(l.parent_id, [...(childrenOf.get(l.parent_id) ?? []), l])
  }
  const byName = (a: Location, b: Location) => a.name.localeCompare(b.name)
  const rows: LocationRow[] = []
  const visited = new Set<number>()
  const walk = (loc: Location, depth: number) => {
    if (visited.has(loc.id)) return
    visited.add(loc.id)
    rows.push({ loc, depth })
    for (const child of (childrenOf.get(loc.id) ?? []).sort(byName)) walk(child, depth + 1)
  }
  for (const r of [...roots].sort(byName)) walk(r, 0)
  for (const l of locations.filter(l => !visited.has(l.id)).sort(byName)) walk(l, 0)
  return rows
}

// Depth of each location for indented <option> lists (0 = root). The walk
// carries a visited set so a corrupt parent cycle can't loop forever.
export function locationDepthOptions(locations: Location[]): LocationOption[] {
  const byId = new Map(locations.map(l => [l.id, l]))
  const depth = (loc: Location): number => {
    let d = 0
    const visited = new Set<number>([loc.id])
    let cur = loc
    while (cur.parent_id !== null && !visited.has(cur.parent_id)) {
      const parent = byId.get(cur.parent_id)
      if (!parent) break
      visited.add(cur.parent_id)
      d++
      cur = parent
    }
    return d
  }
  return locations
    .map(l => ({ id: l.id, name: l.name, depth: depth(l) }))
    .sort((a, b) => a.depth - b.depth || a.name.localeCompare(b.name))
}
