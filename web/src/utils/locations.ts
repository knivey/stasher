import type { Location } from '../api/types'

export interface LocationOption { id: number; name: string; depth: number }

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
