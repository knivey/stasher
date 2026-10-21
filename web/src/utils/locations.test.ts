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
