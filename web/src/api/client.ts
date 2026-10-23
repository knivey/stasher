import type { ProjectBookmark, ProjectComment, ProjectFile, ProjectNote } from './types'

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: (() => void) | null) { onUnauthorized = fn }

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  return handle<T>(resp)
}

async function handle<T>(resp: Response): Promise<T> {
  if (resp.status === 204) return undefined as T
  const data = await resp.json().catch(() => null)
  if (!resp.ok) {
    const e = data?.error
    if (resp.status === 401 && onUnauthorized) onUnauthorized()
    throw new ApiError(resp.status, e?.code ?? 'unknown', e?.message ?? resp.statusText)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body),
  del: (path: string) => request<void>('DELETE', path),
  upload: async <T>(path: string, field: string, file: Blob, filename: string): Promise<T> => {
    const fd = new FormData()
    fd.append(field, file, filename)
    return handle<T>(await fetch(path, { method: 'POST', body: fd }))
  },
}

// Location pictures: a location has at most one picture and an upload
// REPLACES it server-side (api-notes "Location pictures").
export const addLocationPhoto = (id: number, file: File) =>
  api.upload<{ photo_key: string; thumb_key: string | null }>(
    `/api/v1/locations/${id}/photo`, 'file', file, file.name)

export const removeLocationPhoto = (id: number) =>
  api.del(`/api/v1/locations/${id}/photo`)

// Projects flesh-out: notes, comments, materials, bookmarks (api-notes "Projects").
export const addProjectNote = (id: number, body: string) =>
  api.post<ProjectNote>(`/api/v1/projects/${id}/notes`, { body })
export const deleteProjectNote = (id: number, nid: number) =>
  api.del(`/api/v1/projects/${id}/notes/${nid}`)
export const addProjectComment = (id: number, body: string) =>
  api.post<ProjectComment>(`/api/v1/projects/${id}/comments`, { body })
export const deleteProjectComment = (id: number, cid: number) =>
  api.del(`/api/v1/projects/${id}/comments/${cid}`)
export const uploadProjectFile = (id: number, file: File) =>
  api.upload<ProjectFile>(`/api/v1/projects/${id}/files`, 'file', file, file.name)
export const deleteProjectFile = (id: number, fid: number) =>
  api.del(`/api/v1/projects/${id}/files/${fid}`)
export const addProjectBookmark = (id: number, url: string) =>
  api.post<ProjectBookmark>(`/api/v1/projects/${id}/bookmarks`, { url })
export const patchProjectBookmark = (id: number, bid: number, body: { url?: string; title?: string }) =>
  api.patch<ProjectBookmark>(`/api/v1/projects/${id}/bookmarks/${bid}`, body)
export const deleteProjectBookmark = (id: number, bid: number) =>
  api.del(`/api/v1/projects/${id}/bookmarks/${bid}`)
export const archiveProjectBookmark = (id: number, bid: number) =>
  api.post<{ job_id: string }>(`/api/v1/projects/${id}/bookmarks/${bid}/archive`)
