// Bulk location naming rules — a TS mirror of internal/api/location_names.go.
// Both sides are pinned by the same test cases (letters past Z, verbose
// joins, prefix-on-leaf-only) so they cannot drift apart silently.

export type BulkStructure = 'flat' | 'nested'
export type BulkNaming = 'compact' | 'verbose'

export interface BulkLocationNode {
  name: string
  /** joined 1-based axis indices ("1.2") — identifies the node */
  path: string
  /** path of the parent node; '' = the new root location */
  parent: string
  /** 1..dims.length (leaves sit at depth dims.length) */
  depth: number
}

export function spreadsheetLetters(i: number, lower = false): string {
  let out = ''
  while (i > 0) {
    i--
    out = String.fromCharCode((lower ? 97 : 65) + (i % 26)) + out
    i = Math.floor(i / 26)
  }
  return out
}

function compactAxisLabel(dim: number, i: number, single: boolean): string {
  if (single || dim === 1) return String(i)
  if (dim === 0) return spreadsheetLetters(i)
  return spreadsheetLetters(i, true)
}

function verboseAxisLabel(dim: number, i: number, single: boolean): string {
  if (single) return `Slot ${i}`
  const names = ['Row', 'Col', 'Bin']
  return `${names[Math.min(dim, 2)]} ${i}`
}

export function locationNodeName(indices: number[], naming: BulkNaming, single: boolean): string {
  const parts = indices.map((i, d) =>
    naming === 'verbose' ? verboseAxisLabel(d, i, single) : compactAxisLabel(d, i, single))
  return parts.join(naming === 'verbose' ? ' ' : '')
}

function prefixedLeaf(name: string, prefix: string): string {
  return prefix ? `${prefix} ${name}` : name
}

export function generateBulkLocations(
  dims: number[], structure: BulkStructure, naming: BulkNaming, prefix: string,
): BulkLocationNode[] {
  const single = dims.length === 1
  const out: BulkLocationNode[] = []
  const idx: number[] = new Array(dims.length).fill(0)
  if (structure === 'flat') {
    let total = 1
    for (const d of dims) total *= d
    for (let n = 0; n < total; n++) { // last dim varies fastest
      let rem = n
      const path: string[] = new Array(dims.length)
      for (let d = dims.length - 1; d >= 0; d--) {
        idx[d] = (rem % dims[d]) + 1
        path[d] = String(idx[d])
        rem = Math.floor(rem / dims[d])
      }
      out.push({
        name: prefixedLeaf(locationNodeName(idx, naming, single), prefix),
        path: path.join('.'), parent: '', depth: dims.length,
      })
    }
    return out
  }
  // nested: depth-first, so every parent is emitted ahead of its children
  const rec = (depth: number, parentPath: string): void => {
    const leaf = depth === dims.length - 1
    for (let i = 1; i <= dims[depth]; i++) {
      idx[depth] = i
      const path = parentPath ? `${parentPath}.${i}` : String(i)
      let name = locationNodeName(idx.slice(0, depth + 1), naming, single)
      if (leaf) name = prefixedLeaf(name, prefix)
      out.push({ name, path, parent: parentPath, depth: depth + 1 })
      if (!leaf) rec(depth + 1, path)
    }
  }
  rec(0, '')
  return out
}

// Mirrors the backend's bulk validation (docs/api-notes.md): 1-3 dims,
// each 1-100, product <= 1000. Returns the backend's message or null.
export function validateBulkDims(dims: number[]): string | null {
  if (dims.length < 1 || dims.length > 3) return 'dims must contain 1 to 3 entries'
  for (const d of dims) {
    if (d < 1 || d > 100) return 'each dim must be between 1 and 100'
  }
  let product = 1
  for (const d of dims) product *= d
  if (product > 1000) return 'product of dims must be at most 1000'
  return null
}

export interface BulkPreview {
  count: number
  first: string[]
  /** more leaves exist than the (at most five) names shown */
  more: boolean
  error: string | null
}

// Live-preview data: the total child count the backend will create
// (nested mode includes intermediate levels), the first generated leaf
// names, and whether any leaves are hidden beyond those five.
export function bulkPreview(
  dims: number[], structure: BulkStructure, naming: BulkNaming, prefix: string,
): BulkPreview {
  const error = validateBulkDims(dims)
  if (error) return { count: 0, first: [], more: false, error }
  const nodes = generateBulkLocations(dims, structure, naming, prefix.trim())
  const leaves = nodes.filter(n => n.depth === dims.length)
  return {
    count: nodes.length,
    first: leaves.slice(0, 5).map(n => n.name),
    more: leaves.length > 5,
    error: null,
  }
}
