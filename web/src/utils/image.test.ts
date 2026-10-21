import { describe, it, expect, vi } from 'vitest'
import { downscaleImage } from './image'

function fakeBitmap(w: number, h: number) { return { width: w, height: h, close() {} } }

describe('downscaleImage', () => {
  it('passes through small files untouched', async () => {
    const file = new File(['x'], 'a.png', { type: 'image/png' })
    const spy = vi.fn()
    vi.stubGlobal('createImageBitmap', spy)
    expect(await downscaleImage(file)).toBe(file)
    expect(spy).not.toHaveBeenCalled()
  })

  it('scales large images to fit maxDim', async () => {
    const file = new File(['x'], 'big.png', { type: 'image/png' })
    Object.defineProperty(file, 'size', { value: 9_000_000 })
    const draw = vi.fn()
    const ctx = { drawImage: draw }
    const canvas = { width: 0, height: 0, getContext: () => ctx,
      toBlob: (cb: (b: Blob) => void) => cb(new Blob(['j'], { type: 'image/jpeg' })) }
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(fakeBitmap(4000, 2000)))
    const creator = vi.spyOn(document, 'createElement').mockReturnValue(canvas as unknown as HTMLCanvasElement)
    const out = await downscaleImage(file, 2000, 0.85)
    expect(creator).toHaveBeenCalledWith('canvas')
    expect(canvas.width).toBe(2000)   // 4000 scaled to fit
    expect(canvas.height).toBe(1000)
    expect(out.type).toBe('image/jpeg')
  })
})
