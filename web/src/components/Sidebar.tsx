// Left column: add button, sections, your devices by place — the Pocket Calendar sidebar.
import { Link, NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import type { Device } from '../lib/types'
import { Icon } from './icons'
import type { CreateKind } from './Layout'

export function Sidebar({ onCreate, onNavigate, devices }: { onCreate: (k: CreateKind) => void; onNavigate: () => void; devices: Device[] | null }) {
  const { status, settings } = useApp()
  const title = settings?.title || status?.title || 'Pocket Devices'
  const list = devices || []
  const online = list.filter(d => d.state === 'online').length
  const offline = list.filter(d => d.state === 'offline').length
  const waiting = list.filter(d => d.pending > 0 || d.queued > 0).length
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: string }[] = [
    { to: '/', label: 'All devices', icon: 'cpu', end: true, count: list.length ? String(list.length) : undefined },
  ]
  // devices grouped by place, places in order of first appearance (the list is sorted by name)
  const places: [string, Device[]][] = []
  for (const d of list) {
    const p = d.location || 'Other'
    const g = places.find(x => x[0] === p)
    if (g) g[1].push(d)
    else places.push([p, [d]])
  }
  places.sort((a, b) => (a[0] === 'Other' ? 1 : b[0] === 'Other' ? -1 : a[0].localeCompare(b[0])))
  return (
    <aside className="sidebar" aria-label="Devices">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Devices' ? title : <>Pocket <b>Devices</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" onClick={() => onCreate('device')}>
        <Icon name="plus" size={15} strokeWidth={2.2} />Add a device
      </button>

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.count && <span className="sb-count">{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      {list.length > 0 && (
        <section className="sb-sec sb-health" aria-label="At a glance">
          <div className="sb-health-row"><span className="dot-state online" />{online} online</div>
          <div className="sb-health-row"><span className={`dot-state ${offline ? 'offline' : 'none'}`} />{offline} offline</div>
          {waiting > 0 && <div className="sb-health-row"><span className="dot-state pending" />{waiting} waiting for a check-in</div>}
        </section>
      )}

      {places.map(([place, ds]) => (
        <section key={place} className="sb-sec">
          <h4 className="sb-h"><span className="sb-h-btn">{place}</span></h4>
          <div className="sb-devs">
            {ds.map(d => (
              <NavLink key={d.id} to={`/devices/${d.id}`} onClick={onNavigate} className={({ isActive }) => `sb-dev ${isActive ? 'active' : ''}`}>
                <span className={`dot-state ${d.state}`} title={d.state} />
                <span className="ellipsis grow">{d.name}</span>
                {d.summary.ringing && <Icon name="bell" size={13} className="ringing" title="Alarm ringing" />}
              </NavLink>
            ))}
          </div>
        </section>
      ))}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pdev:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
