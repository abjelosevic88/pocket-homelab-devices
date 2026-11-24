// One control per setting type of a kind's table: switches, time pickers,
// weekday toggles, selects, number fields with their range, a time-zone
// picker that writes the POSIX string the device wants.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { DAY_KEYS, DAY_SHORT, formatDays, parseDays } from '../lib/devices'
import type { SettingDef } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { Segmented, Switch, useToast } from './ui'

/** A quick check with the table's rules, for feedback while typing (the server checks again). */
export function checkValue(def: SettingDef, v: string): string {
  const range = def.min !== undefined && def.max !== undefined ? `${def.min} to ${def.max}` : ''
  switch (def.type) {
    case 'number': {
      if (!/^[+-]?\d+$/.test(v.trim())) return 'A whole number'
      const n = Number(v)
      if ((def.min !== undefined && n < def.min) || (def.max !== undefined && n > def.max)) return range
      return ''
    }
    case 'coord': {
      if (!/^[+-]?\d{1,3}(\.\d{1,6})?$/.test(v.trim())) return 'Decimal degrees, up to 6 decimals'
      const n = Number(v)
      if ((def.min !== undefined && n < def.min) || (def.max !== undefined && n > def.max)) return range
      return ''
    }
    case 'hhmm': return /^([01]?\d|2[0-3]):[0-5]\d$/.test(v) ? '' : 'A time 00:00 to 23:59'
    case 'text': {
      const n = [...v].length
      if (n === 0 && def.allow_empty) return ''
      if (def.min !== undefined && n < def.min) return def.min === 1 ? 'Cannot be empty' : def.allow_empty ? `Empty, or ${def.min} to ${def.max} characters` : `At least ${def.min} characters`
      if (def.max !== undefined && n > def.max) return `At most ${def.max} characters`
      if (def.format === 'hostname' && !/^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$/.test(v)) return 'Letters, digits and hyphens only'
      if (def.format === 'host' && !/^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$/.test(v)) return 'A host name like pool.ntp.org'
      if (def.format === 'printable' && /[^\x20-\x7e]/.test(v)) return 'Plain letters, digits and punctuation only'
      return ''
    }
  }
  return ''
}

export function SettingControl({ def, value, onChange, disabled }: { def: SettingDef; value: string; onChange: (v: string) => void; disabled?: boolean }) {
  switch (def.type) {
    case 'bool':
      return <Switch checked={value === 'on'} onChange={v => onChange(v ? 'on' : 'off')} label={def.label} disabled={disabled} />
    case 'enum':
      if ((def.options || []).length <= 3) return <Segmented size="sm" value={value} onChange={onChange} options={(def.options || []).map(o => ({ value: o.value, label: o.label, disabled }))} label={def.label} />
      return <Select ariaLabel={def.label} value={value} onChange={onChange} options={def.options || []} disabled={disabled} />
    case 'number':
      if (def.options?.length) return <Select ariaLabel={def.label} value={value} onChange={onChange} options={def.options} disabled={disabled} className="set-short" />
      return (
        <span className="num-field">
          <input type="number" inputMode="numeric" min={def.min} max={def.max} step={1} value={value} disabled={disabled} onChange={e => onChange(e.target.value)} aria-label={def.label} />
          {def.unit && <span className="muted small">{def.unit}</span>}
        </span>
      )
    case 'hhmm':
      return <input type="time" className="time-field" value={value.length === 4 ? '0' + value : value} disabled={disabled} onChange={e => onChange(e.target.value)} aria-label={def.label} />
    case 'days':
      return <DaysField value={value} onChange={onChange} disabled={disabled} />
    case 'coord':
      return <input className="coord-field mono" inputMode="decimal" value={value} disabled={disabled} onChange={e => onChange(e.target.value.replace(',', '.'))} aria-label={def.label} placeholder="44.772186" />
    case 'text':
      if (def.format === 'tz') return <TZField def={def} value={value} onChange={onChange} disabled={disabled} />
      if (def.secret) return <input type="password" value={value} disabled={disabled} onChange={e => onChange(e.target.value)} aria-label={def.label} placeholder="Unchanged" autoComplete="new-password" />
      return <input value={value} disabled={disabled} onChange={e => onChange(e.target.value)} aria-label={def.label} maxLength={def.max ? def.max + 8 : undefined} spellCheck={false} />
  }
  return <input value={value} onChange={e => onChange(e.target.value)} disabled={disabled} />
}

function DaysField({ value, onChange, disabled }: { value: string; onChange: (v: string) => void; disabled?: boolean }) {
  const on = parseDays(value)
  const quick: [string, string][] = [['all', 'Every day'], ['weekdays', 'Mon–Fri'], ['weekend', 'Weekend']]
  return (
    <div className="days-field">
      <div className="days" role="group" aria-label="Days">
        {DAY_KEYS.map((k, i) => (
          <button key={k} type="button" disabled={disabled} className={`day ${on[i] ? 'on' : ''}`} aria-pressed={on[i]} onClick={() => { const n = [...on]; n[i] = !n[i]; onChange(formatDays(n)) }}>{DAY_SHORT[i]}</button>
        ))}
      </div>
      <div className="days-quick">
        {quick.map(([v, l]) => <button key={v} type="button" disabled={disabled} className={`chip ${value === v ? 'on' : ''}`} onClick={() => onChange(v)}>{l}</button>)}
      </div>
    </div>
  )
}

function TZField({ def, value, onChange, disabled }: { def: SettingDef; value: string; onChange: (v: string) => void; disabled?: boolean }) {
  const { toast } = useToast()
  const presets = def.presets || []
  const known = presets.some(p => p.value === value)
  const [custom, setCustom] = useState(!known && value !== '')
  const browser = Intl.DateTimeFormat().resolvedOptions().timeZone
  const fromBrowser = async () => {
    try { const r = await api.get<{ tz: string }>(`${V1}/tz?zone=${encodeURIComponent(browser)}`); onChange(r.tz); setCustom(!presets.some(p => p.value === r.tz)) } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="tz-field">
      {!custom
        ? <Select ariaLabel={def.label} value={known ? value : ''} onChange={v => { if (v === '__custom') setCustom(true); else onChange(v) }} disabled={disabled}
            options={[...presets.map(p => ({ value: p.value, label: p.label, hint: p.value })), { value: '__custom', label: 'Type a POSIX string…' }]} placeholder="Choose a place…" />
        : <input className="mono" value={value} disabled={disabled} onChange={e => onChange(e.target.value)} aria-label={def.label} placeholder="CET-1CEST,M3.5.0,M10.5.0/3" spellCheck={false} />}
      <div className="row wrap tz-actions">
        {browser && <button type="button" className="btn ghost sm" disabled={disabled} onClick={fromBrowser} title={`The time zone of this browser: ${browser}`}><Icon name="globe" size={14} />Use {browser}</button>}
        <button type="button" className="btn ghost sm" disabled={disabled} onClick={() => setCustom(c => !c)}>{custom ? 'Pick from a list' : 'Type it'}</button>
      </div>
    </div>
  )
}
