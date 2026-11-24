// One device: what it reports, its settings (what you want next to what it
// has), commands, its history and how it connects.
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ACTION_LABEL, COMMAND_STATE, fmtReading, fmtUptime, kindIcon, kindOf, seenLabel, settingDef, showValue, signal, stateLabel, useKinds, when } from '../lib/devices'
import type { Command, CommandDef, DeviceDetail, DeviceEvent, Kind, LogEntry, Place, Readings, SettingDef } from '../lib/types'
import { ago, fmtTime } from '../lib/format'
import { Icon } from '../components/icons'
import { Confirm, CoreHint, Empty, Field, Loading, Modal, SettingRow, Spinner, Switch, Tabs, useAsync, useToast } from '../components/ui'
import { announceChange, useCrumb, useOnChange } from '../components/Layout'
import { checkValue, SettingControl } from '../components/SettingControl'
import { SetupSteps } from '../components/AddDevice'
import { LineChart } from '../components/Chart'

type TabId = 'overview' | 'settings' | 'commands' | 'history' | 'connection'
const TABS: { id: TabId; label: string; icon: string }[] = [
  { id: 'overview', label: 'Overview', icon: 'activity' },
  { id: 'settings', label: 'Settings', icon: 'sliders' },
  { id: 'commands', label: 'Commands', icon: 'zap' },
  { id: 'history', label: 'History', icon: 'history' },
  { id: 'connection', label: 'Connection', icon: 'plug' },
]

export default function DevicePage() {
  const { id } = useParams()
  const [sp, setSp] = useSearchParams()
  const tab: TabId = (TABS.find(t => t.id === sp.get('tab'))?.id) || 'overview'
  const kinds = useKinds()
  const [d, setD] = useState<DeviceDetail | null>(null)
  const [err, setErr] = useState('')
  const load = useCallback(() => {
    api.get<DeviceDetail>(`${V1}/devices/${id}`).then(x => { setD(x); setErr('') }).catch(e => setErr((e as Error).message))
  }, [id])
  useEffect(() => { setD(null); load() }, [load])
  useEffect(() => {
    const t = setInterval(() => { if (!document.hidden) load() }, 10000)
    return () => clearInterval(t)
  }, [load])
  useOnChange(load)
  useCrumb(d?.device.name)
  if (err) return <Empty title="Not found" icon="cpu">{err}</Empty>
  if (!d || !kinds) return <Loading />
  const kind = kindOf(kinds, d.device.kind)
  const dev = d.device
  const go = (t: TabId) => setSp(t === 'overview' ? {} : { tab: t }, { replace: true })
  return (
    <div className="page device-page">
      <header className="dev-head">
        <span className={`dev-ico state-${dev.state}`}><Icon name={kindIcon(dev.icon)} size={22} /></span>
        <div className="grow">
          <h2>{dev.name}</h2>
          <div className="dev-sub muted">{[dev.location, dev.kind_name, dev.hw_id].filter(Boolean).join(' · ')}</div>
        </div>
        <div className="dev-badges">
          <span className={`pill state-${dev.state}`}><span className={`dot-state ${dev.state}`} />{stateLabel(dev)}</span>
          {dev.pending > 0 && <span className="pill pending" title="Changes the device has not taken yet">{dev.pending} waiting</span>}
          {dev.alerts.map(a => <span key={a.id} className={`pill alert-${a.level}`}><Icon name={a.level === 'alarm' ? 'alert' : 'bell'} size={13} />{a.label}</span>)}
        </div>
      </header>
      <div className="dev-tabs"><Tabs tabs={TABS} value={tab} onChange={go} /></div>
      {tab === 'overview' && <Overview d={d} kind={kind} reload={load} go={go} />}
      {tab === 'settings' && kind && <SettingsTab d={d} kind={kind} onSaved={x => { setD(x); announceChange() }} />}
      {tab === 'commands' && kind && <CommandsTab d={d} kind={kind} />}
      {tab === 'history' && kind && <HistoryTab d={d} kind={kind} />}
      {tab === 'connection' && kind && <ConnectionTab d={d} kind={kind} onSaved={x => { setD(x); announceChange() }} />}
    </div>
  )
}

// ---- overview ----

function Overview({ d, kind, reload, go }: { d: DeviceDetail; kind?: Kind; reload: () => void; go: (t: TabId) => void }) {
  const dev = d.device
  const s = dev.summary
  const sig = signal(s.rssi)
  const send = useSend(dev.id, reload)
  if (dev.state === 'new') {
    return (
      <div className="stack loose">
        <div className="callout info"><Icon name="plug" size={15} /><div><b>Waiting for the first check-in.</b> Paste the three lines into the device’s console, or fill in its setup page from your phone (you saw both when you added it). Lost the token? Make a new one on the <button type="button" className="linkish" onClick={() => go('connection')}>Connection</button> tab.</div></div>
        {dev.pending > 0 && <p className="muted small">The settings you already chose here go out with the first answer.</p>}
      </div>
    )
  }
  const subs = new Set((kind?.readings || []).map(r => r.sub).filter(Boolean) as string[])
  const readingTiles = (kind?.readings || []).filter(r => r.key !== 'rssi' && !subs.has(r.key) && dev.readings[r.key] !== undefined)
  const alertReadings = new Set(dev.alerts.map(a => kind?.alerts.find(x => x.id === a.id)?.reading))
  const tiles: { key: string; label: string; value: React.ReactNode; sub?: React.ReactNode; icon: string; tone?: string }[] = [
    ...readingTiles.map(r => {
      const v = dev.readings[r.key]
      const sub = r.sub ? kind?.readings.find(x => x.key === r.sub) : undefined
      return { key: r.key, label: r.label, value: fmtReading(r, v), sub: sub && dev.readings[sub.key] !== undefined && dev.readings[sub.key] !== '' ? `${sub.label}: ${fmtReading(sub, dev.readings[sub.key])}` : undefined, icon: r.icon || 'dot', tone: alertReadings.has(r.key) ? 'bad' : r.type === 'bool' && v ? 'on' : '' }
    }),
    { key: '_seen', label: 'Last check-in', value: seenLabel(dev), sub: dev.state === 'online' ? `every ${fmtEvery(dev.poll_s)}` : 'offline', icon: 'refresh', tone: dev.state === 'online' ? 'ok' : 'bad' },
    { key: '_wifi', label: 'WiFi signal', value: sig.label, sub: s.rssi !== undefined ? `${s.rssi} dBm · ${s.ssid || ''}` : '—', icon: 'wifi', tone: sig.bars <= 1 && s.rssi !== undefined ? 'bad' : '' },
    { key: '_up', label: 'Running for', value: fmtUptime(s.uptime_s), sub: s.reset_reason || '', icon: 'power' },
    { key: '_fw', label: 'Firmware', value: s.fw || '—', sub: kind?.name, icon: 'cpu' },
    { key: '_ip', label: 'Address', value: s.ip || dev.last_ip || '—', sub: dev.last_ip && s.ip !== dev.last_ip ? `seen from ${dev.last_ip}` : 'on your network', icon: 'globe' },
  ]
  const alertActions = new Set((kind?.alerts || []).map(a => a.action).filter(Boolean))
  const sleepy = kind?.power === 'battery'
  return (
    <div className="stack loose">
      {dev.alerts.map(a => {
        const cmd = a.action ? kind?.commands.find(c => c.name === a.action) : undefined
        return (
          <div key={a.id} className={`alert-banner ${a.level}`}>
            <Icon name={a.level === 'alarm' ? 'alert' : 'bell'} size={18} />
            <span className="grow"><b>{a.label}.</b> {a.text} <span className="muted-on small">since {ago(a.since)}</span></span>
            {cmd && <button type="button" className={`btn ${a.level === 'alarm' ? 'danger' : ''}`} disabled={send.busy === cmd.name} onClick={() => send.run(cmd.name)}>{send.busy === cmd.name ? <Spinner /> : <Icon name="send" size={15} />}{cmd.label}</button>}
          </div>
        )
      })}
      {dev.pending > 0 && <div className="callout warn"><Icon name="hourglass" size={15} /><div>{dev.pending === 1 ? 'One change waits' : `${dev.pending} changes wait`} for the device’s next check-in{dev.state === 'offline' ? ', when it is back online' : sleepy ? ' (it runs on a battery and sleeps in between)' : ''}. <button type="button" className="linkish" onClick={() => go('settings')}>See them</button></div></div>}
      {Object.keys(dev.config_errors || {}).length > 0 && <div className="callout danger"><Icon name="alert" size={15} /><div>The device refused {Object.entries(dev.config_errors).map(([k, v]) => `${settingDef(kind, k)?.label || k} (${v})`).join(', ')} and kept its old value.</div></div>}
      <div className="tiles">
        {tiles.map(t => (
          <div key={t.key} className={`tile ${t.tone || ''}`}>
            <div className="tile-label"><Icon name={t.icon} size={14} />{t.label}</div>
            <div className="tile-value tnum">{t.value}</div>
            {t.sub && <div className="tile-sub muted">{t.sub}</div>}
          </div>
        ))}
      </div>
      <div className="quick-actions">
        {kind?.commands.filter(c => !c.danger && !alertActions.has(c.name)).map(c => {
          const arg = c.args?.[0]
          const argVal = arg ? Number((arg.default_setting && d.settings.find(x => x.key === arg.default_setting)?.reported) || arg.default) : 0
          return (
            <button key={c.name} type="button" className="btn" disabled={!!send.busy} title={c.help} onClick={() => send.run(c.name, arg ? { [arg.name]: argVal } : undefined)}>
              {send.busy === c.name ? <Spinner /> : <Icon name={COMMAND_ICON[c.name] || 'send'} size={15} />}{c.label}
            </button>
          )
        })}
      </div>
      {kind && kind.events.length > 0 && <Activity id={dev.id} kind={kind} lastSeen={dev.last_seen_at} settings={d.settings} />}
      {kind && <History48 id={dev.id} kind={kind} />}
    </div>
  )
}

const COMMAND_ICON: Record<string, string> = {
  sync_time: 'clock', weather_refresh: 'thermometer', alarm_test: 'volume', identify: 'search', measure_now: 'refresh', clean_fan: 'wind',
  turn_on: 'power', turn_off: 'power', toggle: 'repeat', silence: 'volume-x', calibrate_co2: 'target',
}

function fmtEvery(s: number): string {
  if (s >= 3600 && s % 3600 === 0) return s === 3600 ? 'hour' : `${s / 3600} h`
  if (s >= 120 && s % 60 === 0) return `${s / 60} min`
  return `${s} s`
}

/** What the device told lately: presses, doors, motion. */
function Activity({ id, kind, lastSeen, settings }: { id: number; kind: Kind; lastSeen: string; settings: DeviceDetail['settings'] }) {
  const named = (key?: string) => { if (!key) return ''; const r = settings.find(x => x.key === key); return r?.desired ?? r?.reported ?? '' }
  const { data } = useAsync(() => api.get<DeviceEvent[]>(`${V1}/devices/${id}/events?limit=12`), [id, lastSeen])
  const label = (e: DeviceEvent) => {
    const def = kind.events.find(x => x.type === e.type)
    const parts = (def?.fields || []).map(f => {
      const v = e.data?.[f.name]
      if (!v) return ''
      const o = f.options?.find(x => x.value === v)
      const name = named(o?.setting)
      return o ? (name ? `${o.label} · ${name}` : o.label) : v
    }).filter(Boolean)
    return { def, text: parts.length ? `${def?.label || e.type}: ${parts.join(', ')}` : def?.label || e.type }
  }
  return (
    <section className="card">
      <div className="card-head"><h3>Lately</h3><span className="muted small">what it told as it happened</span></div>
      {!data ? <Loading /> : data.length === 0 ? <p className="muted small">Nothing yet.</p> : (
        <div className="events">
          {data.map(e => {
            const { def, text } = label(e)
            return (
              <div key={e.id} className={`event-row ${def?.notify ? 'loud' : ''}`}>
                <span className="event-ico"><Icon name={def?.icon || 'dot'} size={14} /></span>
                <span className="grow">{text}</span>
                <span className="muted small" title={when(e.at)}>{ago(e.at)}</span>
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}

function History48({ id, kind }: { id: number; kind: Kind }) {
  const { data } = useAsync(() => api.get<Readings>(`${V1}/devices/${id}/readings?hours=48`), [id])
  const charts = kind.readings.filter(r => r.chart && (data?.[r.key]?.length || 0) > 1)
  const xfmt = (x: string) => { const t = new Date(x); return t.getHours() === 0 && t.getMinutes() < 15 ? t.toLocaleDateString(undefined, { weekday: 'short' }) : fmtTime(t) }
  const tipfmt = (x: string) => `${new Date(x).toLocaleDateString(undefined, { weekday: 'short' })} ${fmtTime(new Date(x))}`
  if (!data) return null
  if (!charts.length) return <p className="muted small">The last two days appear here once the device has reported for a while.</p>
  return (
    <div className="two-col charts">
      {charts.map((r, i) => {
        const pts = data[r.key].filter((_, j, a) => a.length < 300 || j % 3 === 0).map(p => ({ x: p.at, y: p.v }))
        const help = r.key === 'rssi' ? 'dBm, higher is better' : r.unit ? `${r.unit} · last 48 hours` : 'last 48 hours'
        return (
          <section key={r.key} className="card">
            <div className="card-head"><h3>{r.label}</h3><span className="muted small">{help}</span></div>
            <LineChart points={pts} height={170} kind={i === 0 ? 'area' : 'line'} color={CHART_COLORS[i % CHART_COLORS.length]} zero={r.min === 0} xfmt={xfmt} tipfmt={tipfmt}
              format={v => v.toLocaleString(undefined, { maximumFractionDigits: r.decimals ?? 0 })} />
          </section>
        )
      })}
    </div>
  )
}

const CHART_COLORS = ['var(--accent)', '#8b5cf6', '#0d9488', '#d97706', '#db2777']

function useSend(id: number, after?: () => void) {
  const { toast } = useToast()
  const [busy, setBusy] = useState('')
  const run = async (name: string, args?: Record<string, number>, confirm = false, confirmAgain = false) => {
    setBusy(name)
    try {
      await api.post<Command>(`${V1}/devices/${id}/commands`, { name, args, confirm, confirm_again: confirmAgain })
      toast('Sent: it reaches the device at its next check-in')
      after?.(); announceChange()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy('') }
  }
  return { busy, run }
}

// ---- settings ----

function SettingsTab({ d, kind, onSaved }: { d: DeviceDetail; kind: Kind; onSaved: (d: DeviceDetail) => void }) {
  const { toast } = useToast()
  const { settings } = useApp()
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [advanced, setAdvanced] = useState(false)
  const [picker, setPicker] = useState(false)
  const [confirmReboot, setConfirmReboot] = useState(false)
  const rows = useMemo(() => Object.fromEntries(d.settings.map(r => [r.key, r])), [d.settings])
  const current = (key: string) => {
    const r = rows[key]
    if (!r) return ''
    if (r.desired !== null) return r.desired
    return r.reported ?? settingDef(kind, key)?.default ?? ''
  }
  const value = (def: SettingDef) => (def.key in draft ? draft[def.key] : def.secret ? '' : current(def.key))
  const set = (key: string, v: string) => {
    setDraft(x => ({ ...x, [key]: v }))
    const def = settingDef(kind, key)!
    setErrors(x => ({ ...x, [key]: def.secret && v === '' ? '' : checkValue(def, v) }))
  }
  const changed = Object.keys(draft).filter(k => { const def = settingDef(kind, k)!; return def.secret ? draft[k] !== '' : draft[k] !== current(k) })
  const invalid = changed.some(k => errors[k])
  const wifiTouched = changed.some(k => kind.settings.find(s => s.key === k)?.warn === 'wifi')
  const rebootTouched = changed.some(k => kind.settings.find(s => s.key === k)?.reboot)
  const save = async (values: Record<string, string>, reset: string[] = []) => {
    setBusy(true)
    try {
      const r = await api.put<DeviceDetail>(`${V1}/devices/${d.device.id}/settings`, { values, reset })
      onSaved(r); setDraft({}); setErrors({})
      toast('Saved: the device takes it at its next check-in')
      return true
    } catch (e) {
      const errs = (e as { data?: { errors?: Record<string, string> } }).data?.errors
      if (errs) setErrors(errs)
      toast((e as Error).message, 'err')
      return false
    } finally { setBusy(false) }
  }
  const saveDraft = async () => {
    const vals: Record<string, string> = {}
    changed.forEach(k => { vals[k] = draft[k] })
    const reboot = rebootTouched
    if (await save(vals) && reboot) setConfirmReboot(true)
  }
  const weatherOn = !!settings?.weather_configured
  const pickPlace = async (p: Place) => {
    setPicker(false)
    const vals: Record<string, string> = { place: p.name.slice(0, 47), lat: p.lat.toFixed(6), lon: p.lon.toFixed(6) }
    if (p.timezone) {
      try { const r = await api.get<{ tz: string }>(`${V1}/tz?zone=${encodeURIComponent(p.timezone)}`); vals.tz = r.tz } catch { /* keep the time zone */ }
    }
    Object.entries(vals).forEach(([k, v]) => set(k, v))
  }
  return (
    <div className="stack loose settings-tab">
      {kind.groups.map(g => {
        const defs = kind.settings.filter(s => s.group === g.id && (advanced || !s.advanced || s.key in draft))
        if (!defs.length) return null
        return (
          <section key={g.id} className="card set-group">
            <div className="card-head">
              <h3>{g.label}</h3>
              {g.id === 'weather' && <button type="button" className="btn sm" disabled={!weatherOn} title={weatherOn ? 'Pick one of your places in Pocket Weather' : 'Needs Pocket Weather (Settings → Connected apps)'} onClick={() => setPicker(true)}><Icon name="pin" size={14} />Pick a place</button>}
            </div>
            {g.id === 'weather' && !weatherOn && <p className="muted small group-note">Pick a place from Pocket Weather and the name, coordinates and time zone are filled in. <CoreHint /></p>}
            {g.id === 'wifi' && <p className="muted small group-note">{kind.warnings.wifi}</p>}
            {g.id === 'server' && <p className="muted small group-note">These are typed in at the device itself; the server never changes them.</p>}
            {defs.map(def => <SettingLine key={def.key} def={def} row={rows[def.key]} value={value(def)} draft={def.key in draft} error={errors[def.key]} busy={busy}
              onChange={v => set(def.key, v)} onReset={() => save({}, [def.key])} onUndo={() => { setDraft(x => { const n = { ...x }; delete n[def.key]; return n }); setErrors(x => ({ ...x, [def.key]: '' })) }} />)}
          </section>
        )
      })}
      <button type="button" className="btn ghost" onClick={() => setAdvanced(a => !a)}><Icon name={advanced ? 'chevron-up' : 'chevron-down'} size={15} />{advanced ? 'Fewer settings' : 'Show advanced settings'}</button>
      {changed.length > 0 && (
        <div className="save-bar">
          <span className="grow">{changed.length === 1 ? 'One change' : `${changed.length} changes`}{invalid ? ' · fix the marked values' : ''}</span>
          {wifiTouched && <span className="save-warn small"><Icon name="wifi-off" size={14} />WiFi login changes</span>}
          <button type="button" className="btn" onClick={() => { setDraft({}); setErrors({}) }} disabled={busy}>Discard</button>
          <button type="button" className="btn primary" onClick={saveDraft} disabled={busy || invalid}>{busy ? 'Saving…' : 'Save'}</button>
        </div>
      )}
      {picker && <PlacePicker onClose={() => setPicker(false)} onPick={pickPlace} />}
      {confirmReboot && <Confirm danger={false} title="Restart the device?" confirmLabel="Restart at the next check-in" message="Some of the settings you changed are used only after a restart. Restart it now?" onCancel={() => setConfirmReboot(false)}
        onConfirm={async () => { setConfirmReboot(false); try { await api.post(`${V1}/devices/${d.device.id}/commands`, { name: 'reboot', confirm: true }); toast('Restart sent') } catch (e) { toast((e as Error).message, 'err') } }} />}
    </div>
  )
}

function SettingLine({ def, row, value, draft, error, busy, onChange, onReset, onUndo }: {
  def: SettingDef; row?: DeviceDetail['settings'][number]; value: string; draft: boolean; error?: string; busy: boolean
  onChange: (v: string) => void; onReset: () => void; onUndo: () => void
}) {
  const reported = row?.reported ?? null
  const differs = row && row.desired !== null && reported !== null && !def.secret && row.desired !== reported
  const meta: React.ReactNode[] = []
  if (def.reboot) meta.push(<span key="r" className="tag warn"><Icon name="rotate" size={12} />Needs a restart</span>)
  if (row?.pending) meta.push(<span key="p" className="tag pending"><Icon name="hourglass" size={12} />Waiting for the device{differs ? ` · it has ${showValue(def, reported)}` : ''}</span>)
  if (row?.error) meta.push(<span key="e" className="tag bad"><Icon name="alert" size={12} />Refused: {row.error}</span>)
  if (def.secret && row) meta.push(<span key="s" className="tag">{row.secret_set ? 'Set here' : reported === '****' ? 'Set at the device' : 'Not set'}</span>)
  const canReset = !def.locked && !def.secret && def.default_ok && !draft && (row?.desired ?? reported) !== null && (row?.desired ?? reported) !== def.default && !(def.type === 'coord' && Number(row?.desired ?? reported) === Number(def.default))
  return (
    <div className={`set-line ${draft ? 'is-draft' : ''} ${error ? 'is-bad' : ''}`}>
      <div className="set-line-text">
        <div className="set-line-label">{def.label}</div>
        {def.help && <div className="set-line-help muted small">{def.help}</div>}
        {meta.length > 0 && <div className="set-line-meta">{meta}</div>}
        {error && <div className="bad small">{error}</div>}
      </div>
      <div className="set-line-ctl">
        {def.locked
          ? <span className="locked-val mono">{def.secret ? (reported ? 'set' : 'empty') : showValue(def, reported)}</span>
          : <SettingControl def={def} value={value} onChange={onChange} disabled={busy} />}
        {draft && <button type="button" className="btn ghost sm icon-only" title="Undo" aria-label="Undo" onClick={onUndo}><Icon name="undo" size={14} /></button>}
        {canReset && <button type="button" className="btn ghost sm icon-only" title={`Back to the default (${showValue(def, def.default)})`} aria-label="Back to the default" onClick={onReset} disabled={busy}><Icon name="rotate" size={14} /></button>}
      </div>
    </div>
  )
}

function PlacePicker({ onClose, onPick }: { onClose: () => void; onPick: (p: Place) => void }) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<Place[] | null>(null)
  const { data } = useAsync(() => api.get<{ connected: boolean; error?: string; places: Place[] }>(`${V1}/weather/places`), [])
  useEffect(() => {
    if (q.trim().length < 2) { setHits(null); return }
    const t = setTimeout(() => { api.get<{ places: Place[] }>(`${V1}/weather/search?q=${encodeURIComponent(q.trim())}`).then(r => setHits(r.places)).catch(() => setHits([])) }, 300)
    return () => clearTimeout(t)
  }, [q])
  const list = hits ?? data?.places ?? []
  return (
    <Modal title="Pick a place" onClose={onClose} size="sm">
      <div className="stack">
        <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Search a town…" aria-label="Search a town" />
        {!data ? <Loading /> : data.error && !hits ? <p className="bad small">{data.error}</p> : (
          <div className="place-list">
            {list.length === 0 && <p className="muted small">{hits ? 'Nothing found.' : 'No places in Pocket Weather yet. Search for one.'}</p>}
            {list.map((p, i) => (
              <button key={`${p.name}${p.lat}${i}`} type="button" className="place-row" onClick={() => onPick(p)}>
                <Icon name={p.is_home ? 'home' : 'pin'} size={15} />
                <span className="grow"><b>{p.name}</b><span className="muted small"> {[p.region, p.country].filter(Boolean).join(', ')}</span></span>
                <span className="muted small mono">{p.lat.toFixed(2)}, {p.lon.toFixed(2)}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    </Modal>
  )
}

// ---- commands ----

function CommandsTab({ d, kind }: { d: DeviceDetail; kind: Kind }) {
  const dev = d.device
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<Command[]>(`${V1}/devices/${dev.id}/commands?limit=30`), [dev.id, dev.last_seen_at])
  const send = useSend(dev.id, reload)
  const [confirm, setConfirm] = useState<{ c: CommandDef; step: 1 | 2 } | null>(null)
  const [sound, setSound] = useState(0)
  const everyday = kind.commands.filter(c => !c.danger)
  const danger = kind.commands.filter(c => c.danger)
  const cancel = async (c: Command) => { try { await api.del(`${V1}/devices/${dev.id}/commands/${c.id}`); reload() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <div className="stack loose">
      <section className="card">
        <div className="card-head"><h3>Now</h3><span className="muted small">Reaches the device at its next check-in (every {dev.poll_s} s)</span></div>
        <div className="cmd-list">
          {everyday.map(c => (
            <div key={c.name} className="cmd-row">
              <div className="grow"><div className="cmd-label">{c.label}</div><div className="muted small">{c.help}</div></div>
              {c.args?.map(a => <input key={a.name} type="number" min={a.min} max={a.max} value={sound} onChange={e => setSound(Number(e.target.value))} className="num-small" aria-label={a.label} title={`${a.label} ${a.min} to ${a.max}`} />)}
              <button type="button" className="btn sm" disabled={!!send.busy} onClick={() => send.run(c.name, c.args?.length ? { [c.args[0].name]: sound } : undefined)}>{send.busy === c.name ? <Spinner /> : <Icon name="send" size={14} />}Send</button>
            </div>
          ))}
        </div>
      </section>
      <section className="card danger-zone">
        <div className="card-head"><h3>Danger zone</h3></div>
        <div className="cmd-list">
          {danger.map(c => (
            <div key={c.name} className="cmd-row">
              <div className="grow"><div className="cmd-label">{c.label}</div><div className="muted small">{c.help}</div></div>
              <button type="button" className={`btn sm ${c.danger === 2 ? 'danger' : ''}`} disabled={!!send.busy} onClick={() => setConfirm({ c, step: 1 })}>{c.label}</button>
            </div>
          ))}
        </div>
      </section>
      <section className="card">
        <div className="card-head"><h3>Sent lately</h3></div>
        {!data ? <Loading /> : data.length === 0 ? <p className="muted small">Nothing sent yet.</p> : (
          <div className="cmd-hist">
            {data.map(c => {
              const def = kind.commands.find(x => x.name === c.name)
              return (
                <div key={c.id} className={`cmd-hist-row st-${c.state}`}>
                  <span className={`cmd-state st-${c.state}`}>{c.state === 'queued' ? <Icon name="hourglass" size={13} /> : c.state === 'done' ? <Icon name="check" size={13} /> : c.state === 'failed' ? <Icon name="x" size={13} /> : <Icon name="send" size={13} />}</span>
                  <span className="grow"><b>{def?.label || c.name}</b>{Object.keys(c.args || {}).length > 0 && <span className="muted"> {Object.values(c.args).join(', ')}</span>}<span className="muted small"> · {COMMAND_STATE[c.state]}{c.result ? ` · ${c.result}` : ''}</span></span>
                  <span className="muted small" title={when(c.created_at)}>{c.actor ? `${c.actor}, ` : ''}{ago(c.created_at)}</span>
                  {c.state === 'queued' && <button type="button" className="btn ghost sm" onClick={() => cancel(c)}>Withdraw</button>}
                </div>
              )
            })}
          </div>
        )}
      </section>
      {confirm && confirm.step === 1 && <Confirm title={`${confirm.c.label}?`} confirmLabel={confirm.c.danger === 2 ? 'Yes, continue' : confirm.c.label} message={confirm.c.help} danger={confirm.c.danger === 2}
        onCancel={() => setConfirm(null)} onConfirm={() => { if (confirm.c.danger === 2) setConfirm({ c: confirm.c, step: 2 }); else { const c = confirm.c; setConfirm(null); send.run(c.name, undefined, true) } }} />}
      {confirm && confirm.step === 2 && <Confirm title="Really erase everything?" confirmLabel="Erase all settings" message={<>This cannot be undone. <b>{dev.name}</b> forgets its WiFi and this server and must be set up again at the device. The settings you set here stay and go out again once it is back.</>}
        onCancel={() => setConfirm(null)} onConfirm={() => { const c = confirm.c; setConfirm(null); send.run(c.name, undefined, true, true) }} />}
    </div>
  )
}

// ---- history ----

function HistoryTab({ d, kind }: { d: DeviceDetail; kind: Kind }) {
  const [items, setItems] = useState<LogEntry[] | null>(null)
  const [more, setMore] = useState(true)
  const load = useCallback(async (before = 0) => {
    const r = await api.get<LogEntry[]>(`${V1}/devices/${d.device.id}/log?limit=60${before ? `&before=${before}` : ''}`)
    setItems(x => (before && x ? [...x, ...r] : r))
    setMore(r.length === 60)
  }, [d.device.id])
  useEffect(() => { load() }, [load])
  if (!items) return <Loading />
  if (!items.length) return <Empty title="Nothing yet" icon="history">Changes, commands and check-ins show up here.</Empty>
  const line = (e: LogEntry) => {
    const def = settingDef(kind, e.key)
    const cmd = kind.commands.find(c => c.name === e.key)
    switch (e.action) {
      case 'setting': case 'reset': case 'device_changed':
        return <><b>{def?.label || e.key}</b>{e.old !== '' || e.new !== '' ? <>: <span className="muted">{showValue(def, e.old)}</span> → {showValue(def, e.new)}</> : null}</>
      case 'refused': return <><b>{def?.label || e.key}</b>{e.new ? <> kept {showValue(def, e.new)}</> : null}: {e.detail}</>
      case 'command': return <><b>{cmd?.label || e.key}</b>{e.detail && <span className="muted"> {e.detail}</span>}</>
      case 'renamed': return <>{e.old} → <b>{e.new}</b></>
      default: return <>{e.detail}</>
    }
  }
  return (
    <div className="stack">
      <div className="log">
        {items.map(e => (
          <div key={e.id} className={`log-row act-${e.action}`}>
            <span className="log-when muted small" title={when(e.at)}>{when(e.at)}</span>
            <span className="log-what"><span className="log-act">{ACTION_LABEL[e.action] || e.action}</span> {line(e)}</span>
            <span className="log-who muted small">{e.actor === 'device' ? 'the device' : e.actor}</span>
          </div>
        ))}
      </div>
      {more && <button type="button" className="btn ghost" onClick={() => load(items[items.length - 1].id)}>Show older</button>}
    </div>
  )
}

// ---- connection ----

function ConnectionTab({ d, kind, onSaved }: { d: DeviceDetail; kind: Kind; onSaved: (d: DeviceDetail) => void }) {
  const dev = d.device
  const navigate = useNavigate()
  const { toast } = useToast()
  const [f, setF] = useState({ name: dev.name, location: dev.location, notes: dev.notes, poll_s: String(dev.poll_s) })
  const [busy, setBusy] = useState(false)
  const [fresh, setFresh] = useState<DeviceDetail | null>(null)
  const [ask, setAsk] = useState<'token' | 'delete' | null>(null)
  const save = async (patch: Record<string, unknown>) => {
    setBusy(true)
    try { onSaved(await api.put<DeviceDetail>(`${V1}/devices/${dev.id}`, patch)); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const newToken = async () => {
    setAsk(null)
    try { const r = await api.post<DeviceDetail>(`${V1}/devices/${dev.id}/token`); setFresh(r); onSaved(r) } catch (e) { toast((e as Error).message, 'err') }
  }
  const del = async () => {
    setAsk(null)
    try { await api.del(`${V1}/devices/${dev.id}`); announceChange(); navigate('/') } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="stack loose">
      <section className="card">
        <div className="card-head"><h3>About this device</h3></div>
        <div className="two-col tight">
          <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} maxLength={80} /></Field>
          <Field label="Place"><input value={f.location} onChange={e => setF({ ...f, location: e.target.value })} maxLength={80} placeholder="Bedroom" /></Field>
        </div>
        <Field label="Notes" className="mt"><textarea rows={2} value={f.notes} onChange={e => setF({ ...f, notes: e.target.value })} placeholder="Where it came from, the case, the wiring…" /></Field>
        <div className="row mt"><button type="button" className="btn primary" disabled={busy} onClick={() => save({ name: f.name, location: f.location, notes: f.notes })}>Save</button></div>
      </section>
      <section className="card">
        <div className="card-head"><h3>Checking in</h3></div>
        <SettingRow title="Check in every" help={`Seconds between two check-ins (${kind.poll.min} to ${kind.poll.max}). Changes and commands reach the device within this time; it counts as offline after three missed check-ins.`}>
          <span className="num-field"><input type="number" min={kind.poll.min} max={kind.poll.max} value={f.poll_s} onChange={e => setF({ ...f, poll_s: e.target.value })} onBlur={() => Number(f.poll_s) !== dev.poll_s && save({ poll_s: Number(f.poll_s) })} /><span className="muted small">s</span></span>
        </SettingRow>
        <SettingRow title="Tell me when it goes offline" help="Through your notification address (Settings → Notifications)."><Switch checked={dev.notify_offline} onChange={v => save({ notify_offline: v })} label="Offline alert" /></SettingRow>
        <dl className="set-dl mt">
          <div><dt>{kind.hw_id.label}</dt><dd className="mono">{dev.hw_id}</dd></div>
          <div><dt>First check-in</dt><dd>{dev.first_seen_at ? when(dev.first_seen_at) : 'not yet'}</dd></div>
          <div><dt>Last check-in</dt><dd>{dev.last_seen_at ? `${when(dev.last_seen_at)} from ${dev.last_ip}` : 'not yet'}</dd></div>
          <div><dt>Settings revision</dt><dd className="tnum">here {dev.desired_rev} · the device has {dev.applied_rev}</dd></div>
        </dl>
      </section>
      <section className="card">
        <div className="card-head"><h3>Token</h3></div>
        <p className="muted small">The device proves who it is with its own token. Only a fingerprint is kept here: <code>{dev.token_hint}…</code>, made {when(dev.token_at)}.</p>
        <div className="console mt">
          {kind.setup.map(l => <div key={l} className="console-line"><code>{l.replace('{url}', d.server_url).replace('{token}', '<the token>')}</code></div>)}
        </div>
        <p className="muted small mt">Or on the device’s own setup page (its WiFi network, from your phone): your WiFi name and password, <b>Server address</b> <code>{d.server_url}</code> and the <b>Token</b>.</p>
        <div className="row wrap mt"><button type="button" className="btn" onClick={() => setAsk('token')}><Icon name="key" size={15} />Make a new token</button></div>
      </section>
      <section className="card danger-zone">
        <div className="card-head"><h3>Remove</h3></div>
        <div className="cmd-row"><div className="grow"><div className="cmd-label">Remove this device</div><div className="muted small">Its settings, commands and history are deleted here; the device itself keeps working on its own, but is no longer accepted by this server.</div></div><button type="button" className="btn sm danger" onClick={() => setAsk('delete')}>Remove</button></div>
      </section>
      {ask === 'token' && <Confirm danger={false} title="Make a new token?" confirmLabel="Make a new token" message="The old token stops working at once: the device cannot check in until you paste the new one." onCancel={() => setAsk(null)} onConfirm={newToken} />}
      {ask === 'delete' && <Confirm title={`Remove ${dev.name}?`} confirmLabel="Remove" message="Its settings, commands and history are deleted. You can add it again later with a new token." onCancel={() => setAsk(null)} onConfirm={del} />}
      {fresh && <Modal title="The new token" onClose={() => setFresh(null)} size="lg" footer={<button type="button" className="btn primary" onClick={() => setFresh(null)}>Done</button>}><SetupSteps detail={fresh} onCopied={() => toast('Copied')} /></Modal>}
    </div>
  )
}
