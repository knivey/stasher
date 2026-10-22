import { describe, it, expect } from 'vitest'
import { locationDepthOptions, locationTreeRows } from './locations'
import type { Location } from '../api/types'

function loc(id: number, name: string, parent_id: number | null): Location {
  return { id, name, parent_id, notes: '', created_at: '', updated_at: '' }
}

describe('locationDepthOptions', () => {
  it('emits tree pre-order: two roots, each child immediately after its parent', () => {
    const opts = locationDepthOptions([
      loc(2, 'Shed', null),
      loc(4, 'Loft', 2),
      loc(1, 'House', null),
      loc(3, 'Attic', 1),
    ])
    expect(opts.map(o => `${'— '.repeat(o.depth)}${o.name}`))
      .toEqual(['House', '— Attic', 'Shed', '— Loft'])
  })

  it('groups a grandchild under its own subtree, not its depth class', () => {
    const opts = locationDepthOptions([
      loc(1, 'House', null),
      loc(2, 'Yard', null),
      loc(3, 'Attic', 1),
      loc(4, 'Box', 3),
      loc(5, 'Shed', 2),
    ])
    expect(opts.map(o => o.name)).toEqual(['House', 'Attic', 'Box', 'Yard', 'Shed'])
    expect(opts.map(o => o.depth)).toEqual([0, 1, 2, 0, 1])
  })

  it('matches locationTreeRows order (same walk drives both)', () => {
    const locations = [
      loc(9, 'Zed', null),
      loc(2, 'Mid', 9),
      loc(1, 'Alpha', null),
      loc(3, 'Deep', 2),
    ]
    expect(locationDepthOptions(locations).map(o => o.id))
      .toEqual(locationTreeRows(locations).map(r => r.loc.id))
  })
})

describe('locationTreeRows with an expanded set (management list)', () => {
  const tree = [
    loc(1, 'House', null),
    loc(2, 'Cabinet', 1),
    loc(3, 'Bin', 2),
    loc(4, 'Shed', null),
  ]

  it('emits only roots when nothing is expanded', () => {
    expect(locationTreeRows(tree, new Set()).map(r => r.loc.name)).toEqual(['House', 'Shed'])
  })

  it('descends only into expanded nodes — children yes, grandchildren no', () => {
    const rows = locationTreeRows(tree, new Set([1]))
    expect(rows.map(r => r.loc.name)).toEqual(['House', 'Cabinet', 'Shed'])
    expect(rows.map(r => r.depth)).toEqual([0, 1, 0])
  })

  it('never resurrects hidden descendants as fallback roots', () => {
    // Cabinet and Bin are hidden (House collapsed) — the corrupt-cycle
    // fallback pass must not re-append them at depth 0.
    expect(locationTreeRows(tree, new Set()).map(r => r.loc.id)).toEqual([1, 4])
  })

  it('without a set, keeps the legacy full walk (select options path)', () => {
    expect(locationTreeRows(tree).map(r => r.loc.name)).toEqual(['House', 'Cabinet', 'Bin', 'Shed'])
  })
})
