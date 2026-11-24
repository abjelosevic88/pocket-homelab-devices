// Kinds of hardware and small helpers for device pages.
import { useEffect, useState } from 'react'
import { api, V1 } from './api'
import type { Device, Kind, ReadingDef, ReadingValue, SettingDef } from './types'
import { ago, fmtDateTime } from './format'

let kindsCache: Kind[] | null = null
let kindsLoad: Promise<Kind[]> | null = null

/** Every kind of hardware this server knows (loaded once). */
export function useKinds(): Kind[] | null {
  const [k, setK] = useState<Kind[] | null>(kindsCache)
  useEffect(() => {
    if (kindsCache) return
    kindsLoad ||= api.get<Kind[]>(`${V1}/kinds`).then(r => (kindsCache = r))
    kindsLoad.then(setK).catch(() => { kindsLoad = null })
  }, [])
  return k
}

export const kindOf = (kinds: Kind[] | null, id: string) => kinds?.find(k => k.id === id)

export function settingDef(kind: Kind | undefined, key: string): SettingDef | undefined {
  return kind?.settings.find(s => s.key === key)
}

/** "online", "offline · 2 h ago", "not connected yet". */
export function stateLabel(d: Device): string {
  if (d.state === 'online') return 'Online'
  if (d.state === 'new') return 'Not connected yet'
  return `Offline · seen ${ago(d.last_seen_at)}`
}

export function seenLabel(d: Device): string {
  if (!d.last_seen_at) return 'never'
  return ago(d.last_seen_at)
}

export function fmtUptime(s?: number): string {
  if (s === undefined || s === null) return '—'
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60)
  if (d) return `${d} d ${h} h`
  if (h) return `${h} h ${m} min`
  return `${m} min`
}

/** WiFi signal in plain words. */
export function signal(rssi?: number): { label: string; bars: number } {
  if (rssi === undefined || rssi === null) return { label: '—', bars: 0 }
  if (rssi >= -55) return { label: 'Excellent', bars: 4 }
  if (rssi >= -65) return { label: 'Good', bars: 3 }
  if (rssi >= -75) return { label: 'Fair', bars: 2 }
  return { label: 'Weak', bars: 1 }
}

/** A reading as people read it: 21.4 °C, Open, 06:30. */
export function fmtReading(r: ReadingDef | undefined, v: ReadingValue | undefined): string {
  if (v === undefined || v === null || v === '') return '—'
  if (!r) return String(v)
  if (r.type === 'bool') return v ? (r.on || 'Yes') : (r.off || 'No')
  if (r.type === 'number' && typeof v === 'number') {
    const n = v.toLocaleString(undefined, { minimumFractionDigits: r.decimals || 0, maximumFractionDigits: r.decimals || 0 })
    return r.unit ? `${n} ${r.unit}` : n
  }
  return r.options?.find(o => o.value === v)?.label || String(v)
}

/** The icon of a kind (its "icon" in the kind file) as an icon of this app. */
export function kindIcon(icon: string | undefined): string {
  return ({ clock: 'alarm-clock' } as Record<string, string>)[icon || ''] || icon || 'cpu'
}

export const POWER_LABEL: Record<string, string> = { battery: 'Battery', usb: 'USB', mains: 'Mains' }
export const CATEGORY_LABEL: Record<string, string> = { clock: 'Clocks', sensor: 'Sensors', button: 'Buttons', switch: 'Switches' }

/** The readings shown on a device's card: the kind's "card" ones, battery last. */
export function cardReadings(kind: Kind | undefined): ReadingDef[] {
  if (!kind) return []
  const out = kind.readings.filter(r => r.card)
  return [...out.filter(r => r.key !== 'battery'), ...out.filter(r => r.key === 'battery')].slice(0, 4)
}

// ---- the days type: all | weekdays | weekend | none | mon,wed | mon-fri ----

export const DAY_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']
export const DAY_SHORT = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']

export function parseDays(v: string | null | undefined): boolean[] {
  const on = Array(7).fill(false)
  const s = (v || '').toLowerCase().replace(/\s/g, '')
  if (s === 'all') return Array(7).fill(true)
  if (s === 'weekdays') return [true, true, true, true, true, false, false]
  if (s === 'weekend') return [false, false, false, false, false, true, true]
  if (!s || s === 'none') return on
  if (s.includes('-') && !s.includes(',')) {
    const [a, b] = s.split('-').map(d => DAY_KEYS.indexOf(d))
    if (a >= 0 && b >= 0) for (let i = a; ; i = (i + 1) % 7) { on[i] = true; if (i === b) break }
    return on
  }
  s.split(',').forEach(d => { const i = DAY_KEYS.indexOf(d); if (i >= 0) on[i] = true })
  return on
}

/** The shortest form the device understands. */
export function formatDays(on: boolean[]): string {
  const n = on.filter(Boolean).length
  if (n === 7) return 'all'
  if (n === 0) return 'none'
  if (on.join() === [true, true, true, true, true, false, false].join()) return 'weekdays'
  if (on.join() === [false, false, false, false, false, true, true].join()) return 'weekend'
  return DAY_KEYS.filter((_, i) => on[i]).join(',')
}

export function daysLabel(v: string | null | undefined): string {
  const s = (v || '').toLowerCase()
  if (s === 'all') return 'every day'
  if (s === 'weekdays') return 'Monday to Friday'
  if (s === 'weekend') return 'Saturday and Sunday'
  if (s === 'none' || !s) return 'no days'
  const on = parseDays(s)
  return DAY_SHORT.filter((_, i) => on[i]).join(', ')
}

/** A value as people read it (on → On, c → °C, seg7 → its label). */
export function showValue(def: SettingDef | undefined, v: string | null | undefined): string {
  if (v === null || v === undefined) return '—'
  if (!def) return v
  if (def.secret) return v ? 'set' : 'empty'
  if (def.type === 'bool') return v === 'on' ? 'On' : 'Off'
  if (def.type === 'days') return daysLabel(v)
  if (def.type === 'decimal' && def.unit) return `${v} ${def.unit}`
  const opt = (def.options || []).find(o => o.value === v)
  if (opt) return opt.label
  if (def.key === 'tz') return (def.presets || []).find(p => p.value === v)?.label.replace(/ \(.*\)$/, '') || v
  if (v === '') return '(empty)'
  return def.unit ? `${v} ${def.unit}` : v
}

export const ACTION_LABEL: Record<string, string> = {
  alert: 'Alert', alert_cleared: 'Alert over',
  added: 'Added', setting: 'Changed', reset: 'Back to default', command: 'Sent', token: 'New token', refused: 'Refused',
  device_changed: 'Changed at the device', offline: 'Went offline', online: 'Online', renamed: 'Renamed',
}

export const COMMAND_STATE: Record<string, string> = { queued: 'Waiting for the next check-in', sent: 'Sent', done: 'Done', failed: 'Failed', cancelled: 'Withdrawn' }

export const when = (iso: string) => (iso ? fmtDateTime(iso) : '')
