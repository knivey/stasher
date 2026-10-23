import { describe, it, expect } from 'vitest'
import { isVideoHost } from './videoHosts'

describe('isVideoHost', () => {
  it.each([
    'https://www.youtube.com/watch?v=x',
    'https://youtu.be/x',
    'https://m.youtube.com/watch?v=x',
    'https://vimeo.com/123',
    'https://www.twitch.tv/videos/1',
  ])('flags video host: %s', u => {
    expect(isVideoHost(u)).toBe(true)
  })

  it.each([
    'https://www.ti.com/lit/ds',
    'https://example.com/youtube',
    'garbage',
  ])('flags non-video: %s', u => {
    expect(isVideoHost(u)).toBe(false)
  })
})
