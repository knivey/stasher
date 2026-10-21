export interface User { id: number; email: string; display_name: string; default_item_visibility: 'private' | 'shared'; is_admin: boolean }
export interface Tag { id: number; name: string; item_count?: number }
export interface Photo { id: number; storage_key: string; caption: string; sort_order: number; created_at: string }
export interface Doc { id: number; storage_key: string; filename: string; mime_type: string; size_bytes: number; created_at: string }
export interface ItemProject { id: number; name: string; status: string; qty_used: number | null }
export interface Item {
  id: number; name: string; description: string; qty: number; unit: string
  location_id: number | null; visibility: 'private' | 'shared'
  photo_count?: number; created_at: string; updated_at: string
  tags?: Tag[]; photos?: Photo[]; documents?: Doc[]; projects?: ItemProject[]; owner_name?: string
}
export interface Location { id: number; name: string; parent_id: number | null; notes: string; created_at: string; updated_at: string }
export interface Breadcrumb { id: number; name: string }
export interface Project { id: number; name: string; status: 'planned' | 'active' | 'done' | 'archived'; notes: string; created_at: string; updated_at: string }
export interface ProjectItem { item_id: number; item_name: string; item_qty: number; item_unit: string; qty_used: number | null; notes: string; added_at: string }
export interface SessionRow { id: number; user_agent: string; last_ip: string; created_at: string; last_seen_at: string; current: boolean }
export interface Invite { id: number; code: string; created_at: string; used_by_email: string; used_at: string | null }
export interface AdminUser extends User { disabled: boolean; created_at: string }
export interface ListResponse<T> { data: T[]; total: number }
