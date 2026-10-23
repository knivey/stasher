import { describe, it, expect, vi, beforeEach } from 'vitest'
import { api, ApiError, setUnauthorizedHandler } from './client'

function mockFetch(status: number, body: unknown) {
  return vi.fn().mockResolvedValue(
    new Response(body === undefined ? null : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
}

beforeEach(() => setUnauthorizedHandler(null))

describe('api client', () => {
  it('returns parsed JSON on ok', async () => {
    vi.stubGlobal('fetch', mockFetch(200, { data: [{ id: 1 }], total: 1 }))
    const res = await api.get('/api/v1/items')
    expect(res).toEqual({ data: [{ id: 1 }], total: 1 })
  })

  it('returns undefined on 204', async () => {
    vi.stubGlobal('fetch', mockFetch(204, undefined))
    expect(await api.del('/api/v1/tags/1')).toBeUndefined()
  })

  it('throws ApiError with code+message from error shape', async () => {
    vi.stubGlobal('fetch', mockFetch(409, { error: { code: 'tag_exists', message: 'tag already exists' } }))
    const err = (await api.post('/api/v1/items', { name: 'x' }).catch(e => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(409)
    expect(err.code).toBe('tag_exists')
    expect(err.message).toBe('tag already exists')
  })

  it('fires unauthorized handler once on 401', async () => {
    const on401 = vi.fn()
    setUnauthorizedHandler(on401)
    vi.stubGlobal('fetch', mockFetch(401, { error: { code: 'unauthorized', message: 'authentication required' } }))
    await expect(api.get('/api/v1/auth/me')).rejects.toBeInstanceOf(ApiError)
    expect(on401).toHaveBeenCalledTimes(1)
  })

  it('upload posts FormData', async () => {
    const f = vi.fn().mockResolvedValue(new Response('{"id":1}', { status: 201 }))
    vi.stubGlobal('fetch', f)
    const blob = new Blob(['x'], { type: 'image/png' })
    await api.upload('/api/v1/items/1/photos', 'file', blob, 'p.png')
    const [url, init] = f.mock.calls[0]
    expect(url).toBe('/api/v1/items/1/photos')
    expect(init.body).toBeInstanceOf(FormData)
    expect(init.body.get('file')).toBeTruthy()
  })
})
