export interface User { id: number; email: string; display_name: string; default_item_visibility: 'private' | 'shared'; is_admin: boolean }
export interface Tag { id: number; name: string; item_count?: number }
export interface Photo { id: number; storage_key: string; thumb_key?: string | null; caption: string; sort_order: number; created_at: string }
export interface Doc { id: number; storage_key: string; filename: string; mime_type: string; size_bytes: number; created_at: string }
export interface ItemProject { id: number; name: string; status: string; qty_used: number | null }
export interface Note { id: number; body: string; created_at: string }
export interface Item {
  id: number; name: string; description: string; qty: number; unit: string
  location_id: number | null; visibility: 'private' | 'shared'
  disposed?: boolean
  // detail-only: absent on list rows; null when unset
  product_url?: string | null
  photo_count?: number; thumb_key?: string | null
  created_at: string; updated_at: string
  tags?: Tag[]; photos?: Photo[]; documents?: Doc[]; projects?: ItemProject[]; owner_name?: string
  notes?: Note[]
}
// External fetch job (api-notes "External fetch & jobs"): in-memory only —
// a 404 mid-poll means the server restarted and the document was NOT created.
export interface FetchJob {
  status: 'running' | 'done' | 'failed'
  error?: string
  document_id?: number
}
// Document finder (api-notes "Document search"): results contain ONLY
// candidates the server verified as downloadable PDFs; `size` is ABSENT
// when the candidate server didn't report a length (never -1). `rejected`
// counts candidates dropped by verification. Picking one downloads it via
// the ordinary fetch flow.
export interface DocSearchResult { title: string; url: string; host: string; size?: number }
export interface DocSearchResponse { results: DocSearchResult[]; rejected: number }
export interface LedgerEntry {
  id: number; delta: number; resulting_qty: number
  kind: 'used' | 'sold' | 'lost' | 'found' | 'restock' | 'disposed' | 'adjustment'
  project_id: number | null
  // server-resolved project name; null when the project was deleted
  // (absent in old payloads)
  project_name?: string | null
  note: string; created_at: string
}
export interface Location {
  id: number; name: string; parent_id: number | null; notes: string
  // single optional picture; thumb is the derived file (null when generation failed)
  photo_key: string | null; photo_thumb_key: string | null
  created_at: string; updated_at: string
}
export interface Breadcrumb { id: number; name: string }
export interface Project { id: number; name: string; status: 'planned' | 'active' | 'done' | 'archived'; notes: string; visibility: 'private' | 'shared'; created_at: string; updated_at: string }
// Owner BOM row (has item ids/qty). `shared` mirrors the item's visibility.
export interface ProjectItem { item_id: number; item_name: string; item_qty: number; item_unit: string; qty_used: number | null; notes: string; added_at: string; shared: boolean }
// Same rows under the detail-projection name (tasks 9–11 consume this name).
export type ProjectOwnerItem = ProjectItem
// What a sharee sees: masked rows — no item ids/qty/notes, name + usage only.
export type ProjectViewerItem = { item_name: string; qty_used: number | null; shared: boolean }
export interface ProjectNote { id: number; body: string; created_at: string }
export interface ProjectComment { id: number; author: string; mine: boolean; body: string; created_at: string }
export interface ProjectFile {
  id: number; storage_key: string; thumb_key: string | null
  filename: string; mime_type: string; size_bytes: number; created_at: string
}
export interface ProjectBookmark {
  id: number; url: string; title: string; created_at: string
  archived_file: ProjectFile | null
}
// GET /projects/{id}: `notes_log` is owner-only — the key is ABSENT on the
// sharee projection, and sharees get masked (viewer) item rows.
export interface ProjectDetail {
  project: Project; owner_view: boolean
  items: (ProjectOwnerItem | ProjectViewerItem)[]
  notes_log?: ProjectNote[]
  comments: ProjectComment[]; files: ProjectFile[]; bookmarks: ProjectBookmark[]
}
// Row of GET /shared/projects (projects other users shared with you).
export interface SharedProject { id: number; name: string; status: string; owner_name: string; updated_at: string }
export interface SessionRow { id: number; user_agent: string; last_ip: string; created_at: string; last_seen_at: string; current: boolean }
export interface Invite { id: number; code: string; created_at: string; used_by_email: string; used_at: string | null }
export interface AdminUser extends User { disabled: boolean; created_at: string }
export interface ListResponse<T> { data: T[]; total: number }
// AI autofill: POST /autofill proposes item fields from photos. Every key
// is OPTIONAL — present only when the model returned a usable value
// (api-notes "AI autofill"); nothing is stored server-side.
export interface AutofillFields {
  name?: string
  description?: string
  qty?: number
  unit?: string
  tags?: string[]
}
// thumb_key is returned by /trash but /files/{key} 404s while the item is
// trashed (photo rows cascade on delete) — never render an <img> from it.
export interface TrashEntry {
  id: number; name: string; qty: number
  photo_count: number; thumb_key: string | null
  trashed_at: string; purges_at: string | null
}
