import type { Location } from '../api/types'

export interface LocationOption { id: number; name: string; depth: number }
export interface LocationRow { loc: Location; depth: number }

// Flattened tree rows for the locations management view: each root in name
// order, immediately followed by its descendants in name order (path-name
// order). The visited set keeps a corrupt parent chain from recursing
// forever — but where corrupt members land depends on reachability: a pure
// cycle (no path from any root) is appended at the end by the final pass
// (first member at depth 0, the rest nested beneath it), while a cycle
// reached through a tail under a real root is walked in place. Either way
// every location renders exactly once.
//
// `expanded` (optional) collapses the management list: a node whose id is
// NOT in the set contributes its row but none of its descendants — they are
// still marked visited, so the fallback pass cannot resurrect them as fake
// roots. Undefined = the full walk (the select options path).
export function locationTreeRows(locations: Location[], expanded?: Set<number>): LocationRow[] {
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
  const walk = (loc: Location, depth: number, emit: boolean) => {
    if (visited.has(loc.id)) return
    visited.add(loc.id)
    if (emit) rows.push({ loc, depth })
    // Collapsed stops the descent: children are still walked (so they count
    // as visited and never leak into the fallback pass) but emit nothing.
    const descend = emit && (!expanded || expanded.has(loc.id))
    for (const child of (childrenOf.get(loc.id) ?? []).sort(byName)) walk(child, depth + 1, descend)
  }
  for (const r of [...roots].sort(byName)) walk(r, 0, true)
  for (const l of locations.filter(l => !visited.has(l.id)).sort(byName)) walk(l, 0, true)
  return rows
}

// Indented <option> lists, in TREE PRE-ORDER: each parent immediately
// followed by its own descendants (same walk as locationTreeRows), so the
// select mirrors the tree instead of a depth-major alphabet that files
// children under the wrong parent visually. Detached corrupt-cycle members
// (no path from any root) surface at the end, exactly once; ones reached
// through a tail render in place.
export function locationDepthOptions(locations: Location[]): LocationOption[] {
  return locationTreeRows(locations).map(r => ({ id: r.loc.id, name: r.loc.name, depth: r.depth }))
}
