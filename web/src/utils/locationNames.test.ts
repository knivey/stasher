import { describe, it, expect } from 'vitest'
import {
  spreadsheetLetters,
  locationNodeName,
  generateBulkLocations,
  validateBulkDims,
  bulkPreview,
} from './locationNames'

// The naming rules mirror internal/api/location_names.go — same cases,
// so the Go rules and the TS mirror can't drift apart silently.

describe('spreadsheetLetters', () => {
  it('renders upper letters 1->A, 26->Z, 27->AA, 702->ZZ', () => {
    expect(spreadsheetLetters(1)).toBe('A')
    expect(spreadsheetLetters(26)).toBe('Z')
    expect(spreadsheetLetters(27)).toBe('AA')
    expect(spreadsheetLetters(52)).toBe('AZ')
    expect(spreadsheetLetters(53)).toBe('BA')
    expect(spreadsheetLetters(702)).toBe('ZZ')
    expect(spreadsheetLetters(703)).toBe('AAA')
  })

  it('renders lower letters 1->a, 27->aa', () => {
    expect(spreadsheetLetters(1, true)).toBe('a')
    expect(spreadsheetLetters(26, true)).toBe('z')
    expect(spreadsheetLetters(27, true)).toBe('aa')
    expect(spreadsheetLetters(28, true)).toBe('ab')
  })
})

describe('locationNodeName', () => {
  it('labels every axis in compact and verbose modes', () => {
    expect(locationNodeName([1], 'compact', true)).toBe('1')
    expect(locationNodeName([12], 'compact', true)).toBe('12')
    expect(locationNodeName([3], 'verbose', true)).toBe('Slot 3')
    expect(locationNodeName([3, 2], 'compact', false)).toBe('C2')
    expect(locationNodeName([27, 1], 'compact', false)).toBe('AA1')
    expect(locationNodeName([3, 2], 'verbose', false)).toBe('Row 3 Col 2')
    expect(locationNodeName([1, 2, 3], 'compact', false)).toBe('A2c')
    expect(locationNodeName([1, 1, 27], 'compact', false)).toBe('A1aa')
    expect(locationNodeName([3, 2, 1], 'verbose', false)).toBe('Row 3 Col 2 Bin 1')
    expect(locationNodeName([2], 'compact', false)).toBe('B')
    expect(locationNodeName([2], 'verbose', false)).toBe('Row 2')
  })

  it('custom labels replace the default words; empty falls back; compact ignores them', () => {
    expect(locationNodeName([3], 'verbose', true, ['Drawer'])).toBe('Drawer 3')
    expect(locationNodeName([3], 'verbose', true, [''])).toBe('Slot 3')
    expect(locationNodeName([3, 2], 'verbose', false, ['Drawer', 'Tray'])).toBe('Drawer 3 Tray 2')
    expect(locationNodeName([3, 2], 'verbose', false, ['', 'Tray'])).toBe('Row 3 Tray 2')
    expect(locationNodeName([3, 2], 'compact', false, ['Drawer', 'Tray'])).toBe('C2')
  })
})

describe('generateBulkLocations', () => {
  it('flat 2d: one leaf per combination, dim0 outermost, prefix on every leaf', () => {
    const got = generateBulkLocations([2, 3], 'flat', 'compact', 'Bin')
      .map(n => `${n.name}|${n.parent}`).join('; ')
    expect(got).toBe('Bin A1|; Bin A2|; Bin A3|; Bin B1|; Bin B2|; Bin B3|')
  })

  it('flat 3d compact joins letters, numbers, lower letters', () => {
    const got = generateBulkLocations([2, 2, 2], 'flat', 'compact', '').map(n => n.name)
    expect(got).toEqual(['A1a', 'A1b', 'A2a', 'A2b', 'B1a', 'B1b', 'B2a', 'B2b'])
  })

  it('nested: intermediates unprefixed, leaves prefixed, parents are axis prefixes', () => {
    const got = generateBulkLocations([2, 2], 'nested', 'compact', 'Bin')
      .map(n => `${n.name}|${n.parent}`).join('; ')
    expect(got).toBe('A|; Bin A1|1; Bin A2|1; B|; Bin B1|2; Bin B2|2')
  })

  it('nested verbose builds names level by level', () => {
    const got = generateBulkLocations([2, 2], 'nested', 'verbose', '').map(n => n.name)
    expect(got).toEqual(['Row 1', 'Row 1 Col 1', 'Row 1 Col 2', 'Row 2', 'Row 2 Col 1', 'Row 2 Col 2'])
  })

  it('1d compact is plain numbers and verbose is Slot n', () => {
    expect(generateBulkLocations([3], 'flat', 'compact', '').map(n => n.name)).toEqual(['1', '2', '3'])
    expect(generateBulkLocations([3], 'flat', 'verbose', '').map(n => n.name)).toEqual(['Slot 1', 'Slot 2', 'Slot 3'])
  })

  it('custom labels flow through verbose names; empty falls back to the axis default', () => {
    expect(generateBulkLocations([2, 2], 'flat', 'verbose', '', ['Drawer', 'Tray']).map(n => n.name))
      .toEqual(['Drawer 1 Tray 1', 'Drawer 1 Tray 2', 'Drawer 2 Tray 1', 'Drawer 2 Tray 2'])
    expect(generateBulkLocations([2, 2], 'flat', 'verbose', '', ['', 'Tray']).map(n => n.name))
      .toEqual(['Row 1 Tray 1', 'Row 1 Tray 2', 'Row 2 Tray 1', 'Row 2 Tray 2'])
    expect(generateBulkLocations([2], 'flat', 'verbose', '', ['Drawer']).map(n => n.name))
      .toEqual(['Drawer 1', 'Drawer 2'])
  })

  it('nested verbose labels: intermediates use their own axis word', () => {
    const got = generateBulkLocations([2, 2], 'nested', 'verbose', '', ['Drawer', 'Tray'])
      .map(n => `${n.name}|${n.parent}`).join('; ')
    expect(got).toBe('Drawer 1|; Drawer 1 Tray 1|1; Drawer 1 Tray 2|1; Drawer 2|; Drawer 2 Tray 1|2; Drawer 2 Tray 2|2')
  })

  it('compact output never contains the labels', () => {
    expect(generateBulkLocations([2, 2], 'flat', 'compact', '', ['Drawer', 'Tray']).map(n => n.name))
      .toEqual(['A1', 'A2', 'B1', 'B2'])
  })
})

describe('validateBulkDims', () => {
  it('accepts 1-3 dims of 1-100 with product <= 1000', () => {
    expect(validateBulkDims([10])).toBeNull()
    expect(validateBulkDims([10, 100])).toBeNull()
    expect(validateBulkDims([10, 10, 10])).toBeNull()
  })

  it('rejects the same branches the backend rejects', () => {
    expect(validateBulkDims([])).toContain('1 to 3')
    expect(validateBulkDims([1, 2, 3, 4])).toContain('1 to 3')
    expect(validateBulkDims([0, 2])).toContain('1 and 100')
    expect(validateBulkDims([101])).toContain('1 and 100')
    expect(validateBulkDims([11, 91])).toContain('1000')
  })
})

describe('bulkPreview', () => {
  it('counts 12x6 compact flat and shows the first five leaf names', () => {
    const p = bulkPreview([12, 6], 'flat', 'compact', '')
    expect(p.error).toBeNull()
    expect(p.count).toBe(72)
    expect(p.first).toEqual(['A1', 'A2', 'A3', 'A4', 'A5'])
    expect(p.more).toBe(true)
  })

  it('nested counts intermediate levels too; first names are leaves', () => {
    const p = bulkPreview([2, 3], 'nested', 'compact', '')
    expect(p.count).toBe(8) // 2 rows + 6 cells, matching children_created
    expect(p.first).toEqual(['A1', 'A2', 'A3', 'B1', 'B2'])
    expect(p.more).toBe(true) // 6 leaves, only 5 shown
  })

  it('size-1 dims: no ellipsis when every leaf is already listed', () => {
    // 12 nodes (4 per level) but only 4 leaves - nothing is hidden
    const p = bulkPreview([4, 1, 1], 'nested', 'compact', '')
    expect(p.count).toBe(12)
    expect(p.first).toEqual(['A1a', 'B1a', 'C1a', 'D1a'])
    expect(p.more).toBe(false)
  })

  it('returns the validation error instead of names for invalid configs', () => {
    const p = bulkPreview([100, 100], 'flat', 'compact', '')
    expect(p.error).toContain('1000')
    expect(p.count).toBe(0)
    expect(p.first).toEqual([])
    expect(p.more).toBe(false)
  })

  it('exposes every generated node as a row: flat leaves at depth 0', () => {
    const p = bulkPreview([2, 2], 'flat', 'compact', '')
    expect(p.rows).toEqual([
      { name: 'A1', depth: 0 }, { name: 'A2', depth: 0 },
      { name: 'B1', depth: 0 }, { name: 'B2', depth: 0 },
    ])
  })

  it('exposes nested intermediates and leaves in creation order, 0-based depth', () => {
    const p = bulkPreview([2, 2], 'nested', 'compact', '')
    expect(p.rows).toEqual([
      { name: 'A', depth: 0 }, { name: 'A1', depth: 1 }, { name: 'A2', depth: 1 },
      { name: 'B', depth: 0 }, { name: 'B1', depth: 1 }, { name: 'B2', depth: 1 },
    ])
  })

  it('preview rows are empty when the config is invalid', () => {
    const p = bulkPreview([100, 100], 'nested', 'compact', '')
    expect(p.error).toContain('1000')
    expect(p.rows).toEqual([])
  })

  it('preview reflects custom verbose labels', () => {
    const p = bulkPreview([2, 2], 'flat', 'verbose', '', ['Drawer', 'Tray'])
    expect(p.error).toBeNull()
    expect(p.first).toEqual(['Drawer 1 Tray 1', 'Drawer 1 Tray 2', 'Drawer 2 Tray 1', 'Drawer 2 Tray 2'])
    // labels never leak into a compact preview
    expect(bulkPreview([2, 2], 'flat', 'compact', '', ['Drawer', 'Tray']).first)
      .toEqual(['A1', 'A2', 'B1', 'B2'])
  })
})
