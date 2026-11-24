// All your devices: one card each, what matters at a glance, by kind of device.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { CATEGORY_LABEL, cardReadings, fmtReading, kindIcon, kindOf, seenLabel, stateLabel, useKinds } from '../lib/devices'
import type { Device, Kind } from '../lib/types'
import { Icon } from '../components/icons'
import { Loading, Segmented } from '../components/ui'
import { openCreate, useDevices } from '../components/Layout'

export default function Devices() {
  const { devices } = useDevices()
  const kinds = useKinds()
  const [filter, setFilter] = useState('all')
  if (!devices || !kinds) return <Loading />
  if (!devices.length) {
    return (
      <div className="page">
        <div className="welcome">
          <img src="/favicon.svg" alt="" width={72} height={72} />
          <h2>Your devices, in one place</h2>
          <p className="muted">Clocks, sensors, buttons, plugs: add a device, paste three lines into its console, and from then on you change its settings here, see what it measures and hear about it when something is wrong.</p>
          <button type="button" className="btn primary lg" onClick={() => openCreate('device')}><Icon name="plus" size={16} />Add your first device</button>
          <div className="kind-strip">
            {kinds.map(k => <span key={k.id} className="kind-chip" title={k.summary}><Icon name={kindIcon(k.icon)} size={14} />{k.name}</span>)}
          </div>
          <p className="muted small">No hardware at hand? <code>pocket-devices simulate -kind button -url … -id … -token …</code> behaves like one.</p>
        </div>
      </div>
    )
  }
  const online = devices.filter(d => d.state === 'online').length
  const alerting = devices.filter(d => d.alerts.length > 0)
  const cats = Array.from(new Set(devices.map(d => d.category))).filter(Boolean)
  const shown = devices.filter(d => filter === 'all' || (filter === 'alerts' ? d.alerts.length > 0 : d.category === filter))
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow"><h2>Devices</h2><p className="muted small">{devices.length === 1 ? 'One device' : `${devices.length} devices`} · {online} online{alerting.length ? ` · ${alerting.length} with an alert` : ''}</p></div>
        <button type="button" className="btn primary" onClick={() => openCreate('device')}><Icon name="plus" size={15} />Add a device</button>
      </div>
      {(cats.length > 1 || alerting.length > 0) && (
        <div className="scroll-x"><Segmented size="sm" value={filter} onChange={setFilter} label="Show"
          options={[{ value: 'all', label: 'All' }, ...cats.map(c => ({ value: c, label: CATEGORY_LABEL[c] || c })), ...(alerting.length ? [{ value: 'alerts', label: `Alerts (${alerting.length})` }] : [])]} /></div>
      )}
      <div className="dev-grid">
        {shown.map(d => <DeviceCard key={d.id} d={d} kind={kindOf(kinds, d.kind)} />)}
      </div>
    </div>
  )
}

function DeviceCard({ d, kind }: { d: Device; kind?: Kind }) {
  const alert = d.alerts.find(a => a.level === 'alarm') || d.alerts[0]
  const facts = cardReadings(kind)
  return (
    <Link to={`/devices/${d.id}`} className={`dev-card state-${d.state} ${alert ? `alert-${alert.level}` : ''}`}>
      <div className="dev-card-top">
        <span className={`dev-ico state-${d.state}`}><Icon name={kindIcon(d.icon)} size={20} /></span>
        <div className="grow">
          <div className="dev-card-name ellipsis">{d.name}</div>
          <div className="muted small ellipsis">{[d.location, d.kind_name].filter(Boolean).join(' · ')}</div>
        </div>
      </div>
      {alert
        ? <div className={`dev-card-alert ${alert.level}`}><Icon name={alert.level === 'alarm' ? 'alert' : 'bell'} size={14} /><span className="ellipsis">{alert.text || alert.label}</span></div>
        : <div className="dev-card-state"><span className={`dot-state ${d.state}`} />{stateLabel(d)}</div>}
      {d.state !== 'new' && facts.length > 0 && (
        <div className="dev-card-facts">
          {facts.map(r => {
            const sub = r.sub ? kind?.readings.find(x => x.key === r.sub) : undefined
            const v = d.readings[r.key]
            return (
              <span key={r.key} title={r.label}>
                <Icon name={r.icon || 'dot'} size={14} />
                <span className="muted fact-label">{r.label}</span>
                <b className={r.type === 'bool' && v ? 'fact-on' : ''}>{fmtReading(r, v)}{sub && v === true && d.readings[sub.key] !== undefined && d.readings[sub.key] !== '' ? ` · ${fmtReading(sub, d.readings[sub.key])}` : ''}</b>
              </span>
            )
          })}
        </div>
      )}
      <div className="dev-card-foot">
        <span className="muted small">{d.state === 'new' ? 'waiting for the first check-in' : `seen ${seenLabel(d)}`}</span>
        <span className="grow" />
        {d.pending > 0 && <span className="pill pending sm" title="Changes the device has not taken yet">{d.pending} waiting</span>}
        {d.queued > 0 && <span className="pill sm" title="Commands waiting for the next check-in">{d.queued} to send</span>}
        {d.power === 'battery' && <Icon name="battery" size={14} className="muted" title="Runs on a battery" />}
      </div>
    </Link>
  )
}
