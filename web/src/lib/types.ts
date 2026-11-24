export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'

export interface Settings {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string
  device_url: string; device_url_effective: string
  notify_offline: boolean; notify_refused: boolean
  notify_url: string; notify_format: string; notify_configured: boolean
  weather_mode: ConnMode; weather_via: ConnVia; weather_detail: string; weather_open_url: string; weather_configured: boolean
  weather_url: string; weather_token: string; weather_public_url: string
  setup_complete: boolean; base_url: string
}

// ---- kinds of hardware (internal/kinds/*.json) ----

export type SettingType = 'text' | 'number' | 'bool' | 'hhmm' | 'days' | 'enum' | 'coord'
export interface Option { value: string; label: string }
export interface SettingDef {
  key: string; type: SettingType; group: string; label: string; help: string; default: string
  min?: number; max?: number; allow_empty?: boolean; format?: string
  options?: Option[]; presets?: Option[]; unit?: string
  secret?: boolean; locked?: boolean; reboot?: boolean; warn?: string; advanced?: boolean; default_ok: boolean
}
export interface CommandArg { name: string; label: string; type: string; min: number; max: number; default: number }
export interface CommandDef { name: string; label: string; help: string; danger?: number; args?: CommandArg[] }
export interface Kind {
  id: string; name: string; protocol: string; summary: string; icon: string
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

export interface DeviceSummary {
  fw?: string; uptime_s?: number; ssid?: string; ip?: string; rssi?: number; time_synced?: boolean
  temp_c?: number; place?: string; ringing?: boolean; alarm_on?: boolean; alarm_time?: string; alarm_days?: string; temp_unit?: string
}
export interface Device {
  id: number; kind: string; kind_name: string; name: string; hw_id: string; location: string; notes: string
  token_hint: string; token_at: string; poll_s: number; desired_rev: number; applied_rev: number
  first_seen_at: string; last_seen_at: string; last_ip: string
  status: Record<string, unknown>; config_errors: Record<string, string>
  notify_offline: boolean; demo: boolean; created_at: string; updated_at: string
  state: 'online' | 'offline' | 'new'; pending: number; queued: number; summary: DeviceSummary
}
export interface SettingRowT { key: string; desired: string | null; reported: string | null; secret_set?: boolean; pending: boolean; error?: string; changed_at?: string }
export interface DeviceDetail {
  device: Device; settings: SettingRowT[]; server_url: string
  token?: string; setup?: string[]; setup_help?: string
}
export interface Command { id: number; device_id: number; name: string; args: Record<string, unknown>; state: 'queued' | 'sent' | 'done' | 'failed' | 'cancelled'; result: string; actor: string; created_at: string; sent_at: string; done_at: string }
export interface LogEntry { id: number; device_id: number; at: string; actor: string; action: string; key: string; old: string; new: string; detail: string }
export interface Sample { at: string; rssi: number | null; temp_c: number | null }
export interface Place { id?: number; name: string; region: string; country: string; country_code: string; lat: number; lon: number; timezone: string; is_home?: boolean }
