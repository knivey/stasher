export async function downscaleImage(file: File, maxDim = 2000, quality = 0.85): Promise<Blob> {
  if (file.size < 2_000_000) return file
  if (typeof createImageBitmap !== 'function') return file
  const bitmap = await createImageBitmap(file)
  try {
    const scale = Math.min(1, maxDim / Math.max(bitmap.width, bitmap.height))
    if (scale >= 1) return file
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(bitmap.width * scale)
    canvas.height = Math.round(bitmap.height * scale)
    canvas.getContext('2d')!.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
    return await new Promise<Blob>((res, rej) =>
      canvas.toBlob(b => (b ? res(b) : rej(new Error('encode failed'))), 'image/jpeg', quality))
  } finally { bitmap.close?.() }
}
