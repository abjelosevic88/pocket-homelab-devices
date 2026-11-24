export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'

export interface Settings {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string
  device_url: string; device_url_effective: string
  notify_offline: boolean; notify_refused: boolean; notify_alerts: boolean
  notify_url: string; notify_format: string; notify_configured: boolean
  weather_mode: ConnMode; weather_via: ConnVia; weather_detail: string; weather_open_url: string; weather_configured: boolean
  weather_url: string; weather_token: string; weather_public_url: string
  setup_complete: boolean; base_url: string
}

// ---- kinds of hardware (internal/kinds/*.json) ----

export type SettingType = 'text' | 'number' | 'bool' | 'hhmm' | 'days' | 'enum' | 'coord' | 'decimal'
export interface Option { value: string; label: string; setting?: string }
export interface SettingDef {
  key: string; type: SettingType; group: string; label: string; help: string; default: string
  min?: number; max?: number; allow_empty?: boolean; format?: string; decimals?: number
  options?: Option[]; presets?: Option[]; unit?: string
  secret?: boolean; locked?: boolean; reboot?: boolean; warn?: string; advanced?: boolean; default_ok: boolean
}
export interface CommandArg { name: string; label: string; type: string; min: number; max: number; default: number; default_setting?: string }
export interface CommandDef { name: string; label: string; help: string; danger?: number; args?: CommandArg[] }
export interface ReadingDef {
  key: string; path: string; label: string; type: 'number' | 'bool' | 'text'; unit?: string; decimals?: number; icon?: string
  on?: string; off?: string; chart?: boolean; card?: boolean; sub?: string; min?: number; options?: Option[]
}
export interface AlertDef { id: string; reading: string; label: string; level: 'info' | 'warning' | 'alarm'; action?: string }
export interface EventDef { type: string; label: string; icon?: string; fields?: { name: string; label: string; options?: Option[] }[]; notify?: boolean }
export interface Kind {
  id: string; name: string; protocol: string; summary: string; icon: string
  category: 'clock' | 'sensor' | 'button' | 'switch'; power: 'mains' | 'usb' | 'battery'
  readings: ReadingDef[]; alerts: AlertDef[]; events: EventDef[]
  hw_id: { label: string; help: string; example: string; format: string }
  token: { min: number; max: number }
  poll: { default: number; min: number; max: number }
  setup: string[]; setup_help: string
  groups: { id: string; label: string }[]
  settings: SettingDef[]
  commands: CommandDef[]
  warnings: Record<string, string>
}

// ---- devices ----

export interface DeviceSummary { fw?: string; uptime_s?: number; ssid?: string; ip?: string; rssi?: number; reset_reason?: string }
export interface ActiveAlert { id: string; label: string; level: 'info' | 'warning' | 'alarm'; text: string; since: string; action?: string }
export type ReadingValue = number | boolean | string
export interface Device {
  id: number; kind: string; kind_name: string; name: string; hw_id: string; location: string; notes: string
  token_hint: string; token_at: string; poll_s: number; desired_rev: number; applied_rev: number
  first_seen_at: string; last_seen_at: string; last_ip: string
  status: Record<string, unknown>; config_errors: Record<string, string>
  notify_offline: boolean; demo: boolean; created_at: string; updated_at: string
  state: 'online' | 'offline' | 'new'; pending: number; queued: number; summary: DeviceSummary
  readings: Record<string, ReadingValue>; alerts: ActiveAlert[]; icon: string; category: string; power: string
}
export interface SettingRowT { key: string; desired: string | null; reported: string | null; secret_set?: boolean; pending: boolean; error?: string; changed_at?: string }
export interface DeviceDetail {
  device: Device; settings: SettingRowT[]; server_url: string
  token?: string; setup?: string[]; setup_help?: string
}
export interface Command { id: number; device_id: number; name: string; args: Record<string, unknown>; state: 'queued' | 'sent' | 'done' | 'failed' | 'cancelled'; result: string; actor: string; created_at: string; sent_at: string; done_at: string }
export interface LogEntry { id: number; device_id: number; at: string; actor: string; action: string; key: string; old: string; new: string; detail: string }
export type Readings = Record<string, { at: string; v: number }[]>
export interface DeviceEvent { id: number; device_id: number; type: string; data: Record<string, string>; at: string }
export interface Place { id?: number; name: string; region: string; country: string; country_code: string; lat: number; lon: number; timezone: string; is_home?: boolean }
