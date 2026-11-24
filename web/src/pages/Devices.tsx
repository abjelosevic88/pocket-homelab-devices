// All your devices: one card each, what matters at a glance.
import { Link } from 'react-router-dom'
import { fmtTemp, seenLabel, signal, stateLabel, daysLabel } from '../lib/devices'
import type { Device } from '../lib/types'
import { Icon } from '../components/icons'
import { Loading } from '../components/ui'
import { openCreate, useDevices } from '../components/Layout'

export default function Devices() {
  const { devices } = useDevices()
  if (!devices) return <Loading />
  if (!devices.length) {
    return (
      <div className="page">
        <div className="welcome">
          <img src="/favicon.svg" alt="" width={72} height={72} />
          <h2>Your devices, in one place</h2>
          <p className="muted">Add a device, paste three lines into its console, and from then on you change its settings here: the alarm, the brightness, the WiFi, the time zone. It checks in every half minute and tells you how it is.</p>
          <button type="button" className="btn primary lg" onClick={() => openCreate('device')}><Icon name="plus" size={16} />Add your first device</button>
          <p className="muted small">No hardware at hand? <code>pocket-devices simulate -url … -id … -token …</code> behaves like a clock.</p>
        </div>
      </div>
    )
  }
  const online = devices.filter(d => d.state === 'online').length
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow"><h2>Devices</h2><p className="muted small">{devices.length === 1 ? 'One device' : `${devices.length} devices`} · {online} online</p></div>
        <button type="button" className="btn primary" onClick={() => openCreate('device')}><Icon name="plus" size={15} />Add a device</button>
      </div>
      <div className="dev-grid">
        {devices.map(d => <DeviceCard key={d.id} d={d} />)}
      </div>
    </div>
  )
}

function DeviceCard({ d }: { d: Device }) {
  const s = d.summary
  const sig = signal(s.rssi)
  return (
    <Link to={`/devices/${d.id}`} className={`dev-card state-${d.state} ${s.ringing ? 'is-ringing' : ''}`}>
      <div className="dev-card-top">
        <span className={`dev-ico state-${d.state}`}><Icon name="alarm-clock" size={20} /></span>
        <div className="grow">
          <div className="dev-card-name ellipsis">{d.name}</div>
          <div className="muted small ellipsis">{[d.location, d.kind_name].filter(Boolean).join(' · ')}</div>
        </div>
      </div>
      <div className="dev-card-state"><span className={`dot-state ${d.state}`} />{stateLabel(d)}</div>
      {d.state !== 'new' && (
        <div className="dev-card-facts">
          <span title="Outdoor temperature"><Icon name="thermometer" size={14} />{fmtTemp(s.temp_c, s.temp_unit)}</span>
          <span title="Alarm"><Icon name="alarm-clock" size={14} />{s.ringing ? <b className="bad">ringing</b> : s.alarm_on ? `${s.alarm_time}, ${daysLabel(s.alarm_days)}` : 'alarm off'}</span>
          <span title={s.rssi !== undefined ? `${s.rssi} dBm` : ''}><Icon name="wifi" size={14} />{sig.label}</span>
        </div>
      )}
      <div className="dev-card-foot">
        <span className="muted small">{d.state === 'new' ? 'waiting for the first check-in' : `seen ${seenLabel(d)}`}</span>
        <span className="grow" />
        {d.pending > 0 && <span className="pill pending sm" title="Changes the device has not taken yet">{d.pending} waiting</span>}
        {d.queued > 0 && <span className="pill sm" title="Commands waiting for the next check-in">{d.queued} to send</span>}
        {d.demo && <span className="pill sm" title="A simulated clock (demo data)">demo</span>}
      </div>
    </Link>
  )
}
