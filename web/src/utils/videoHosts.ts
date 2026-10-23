// Hosts whose "page" is really a video player. Archiving renders the PAGE
// as a PDF — for these, that's rarely what the user wants, so the archive
// button asks first (see ProjectBookmarksSection).
const VIDEO_HOSTS = new Set([
  'youtube.com', 'www.youtube.com', 'm.youtube.com', 'youtu.be',
  'vimeo.com', 'player.vimeo.com', 'twitch.tv', 'www.twitch.tv',
  'dailymotion.com', 'www.dailymotion.com',
])

export function isVideoHost(url: string): boolean {
  try {
    return VIDEO_HOSTS.has(new URL(url).hostname.toLowerCase())
  } catch {
    return false
  }
}
