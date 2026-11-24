// Add a device: kind, name, hardware id, place — then the token, shown once,
// with the exact console lines to paste into the device.
import React, { useState } from 'react'
import { api, V1 } from '../lib/api'
import { CATEGORY_LABEL, POWER_LABEL, kindIcon, useKinds } from '../lib/devices'
import type { DeviceDetail } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { Field, Loading, Modal, useToast } from './ui'
import { useDevices } from './Layout'

export function AddDeviceModal({ onClose, onAdded }: { onClose: () => void; onAdded: (d: DeviceDetail) => void }) {
  const kinds = useKinds()
  const { devices } = useDevices()
  const { toast } = useToast()
  const [f, setF] = useState({ kind: '', name: '', hw_id: '', location: '' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [made, setMade] = useState<DeviceDetail | null>(null)
  const kind = kinds?.find(k => k.id === f.kind) || (kinds?.length === 1 ? kinds[0] : undefined)
  const places = Array.from(new Set((devices || []).map(d => d.location).filter(Boolean))).sort()
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!kind) return
    setBusy(true); setErr('')
    try {
      const d = await api.post<DeviceDetail>(`${V1}/devices`, { ...f, kind: kind.id, name: f.name.trim() || kind.name })
      setMade(d)
    } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }
  if (made) {
    return (
      <Modal title={`Connect ${made.device.name}`} onClose={() => onAdded(made)} size="lg"
        footer={<button type="button" className="btn primary" onClick={() => onAdded(made)}>Done, I pasted them</button>}>
        <SetupSteps detail={made} onCopied={() => toast('Copied')} />
      </Modal>
    )
  }
  if (kinds && !kind) {
    const cats = Array.from(new Set(kinds.map(k => k.category)))
    return (
      <Modal title="Add a device" onClose={onClose}>
        <div className="stack">
          <p className="muted small">What kind of device is it?</p>
          {cats.map(c => (
            <div key={c} className="kind-group">
              <h4 className="kind-group-h">{CATEGORY_LABEL[c] || c}</h4>
              <div className="kind-grid">
                {kinds.filter(k => k.category === c).map(k => (
                  <button key={k.id} type="button" className="kind-card" onClick={() => setF({ ...f, kind: k.id })}>
                    <span className="kind-card-ico"><Icon name={kindIcon(k.icon)} size={20} /></span>
                    <span className="grow"><b>{k.name}</b><span className="muted small">{POWER_LABEL[k.power] || k.power}{k.events.length ? ` · ${k.events.map(e => e.label.toLowerCase()).slice(0, 2).join(', ')}` : ''}</span></span>
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      </Modal>
    )
  }
  return (
    <Modal title={kind ? `Add a ${kind.name.toLowerCase()}` : 'Add a device'} onClose={onClose} size="sm">
      {!kinds ? <Loading /> : (
        <form onSubmit={submit} className="grid form-stack">
          {kind && <div className="kind-intro"><Icon name={kindIcon(kind.icon)} size={18} /><p className="small muted">{kind.summary}</p></div>}
          {kinds.length > 1 && <button type="button" className="btn ghost sm kind-back" onClick={() => setF({ ...f, kind: '' })}><Icon name="chevron-left" size={14} />Another kind of device</button>}
          <Field label="Name"><input autoFocus value={f.name} onChange={e => setF({ ...f, name: e.target.value })} placeholder={kind ? `${kind.name} in the ${kind.category === 'button' ? 'office' : 'bedroom'}` : ''} maxLength={80} /></Field>
          <Field label={kind?.hw_id.label || 'Hardware id'} help={kind?.hw_id.help}>
            <input value={f.hw_id} onChange={e => setF({ ...f, hw_id: e.target.value })} placeholder={kind?.hw_id.example} spellCheck={false} autoCapitalize="characters" autoComplete="off" className="mono" required />
          </Field>
          <Field group label="Place" help="Where it stands. Devices are grouped by place.">
            <Select ariaLabel="Place" value={f.location} onChange={v => setF({ ...f, location: v })} options={places.map(p => ({ value: p, label: p }))} placeholder="Bedroom, Kitchen…"
              onCreate={label => label.trim()} createLabel="Use" clearLabel="No place" />
          </Field>
          {err && <p className="bad small">{err}</p>}
          <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !f.hw_id.trim()}>{busy ? 'Adding…' : 'Add and get the token'}</button></div>
        </form>
      )}
    </Modal>
  )
}

/** The token (once) and the lines for the device's console. */
export function SetupSteps({ detail, onCopied }: { detail: DeviceDetail; onCopied: () => void }) {
  const lines = detail.setup || []
  const copy = (t: string) => { navigator.clipboard?.writeText(t).then(onCopied).catch(() => {}) }
  const insecure = !detail.server_url.startsWith('https://')
  return (
    <div className="stack">
      <p>{detail.setup_help}</p>
      <div className="console">
        {lines.map(l => (
          <div key={l} className="console-line"><code>{l}</code><button type="button" className="btn ghost sm icon-only" aria-label="Copy this line" title="Copy this line" onClick={() => copy(l)}><Icon name="copy" size={14} /></button></div>
        ))}
      </div>
      <div className="row wrap"><button type="button" className="btn" onClick={() => copy(lines.join('\n'))}><Icon name="copy" size={15} />Copy all three lines</button></div>
      <div className="callout warn"><Icon name="key" size={15} /><div>The token is shown <b>only now</b>. If you lose it, make a new one on the device’s Connection tab; the old one stops working at once.</div></div>
      {insecure && <div className="callout danger"><Icon name="alert" size={15} /><div>The address is not https://. The clock only talks to a server with a real certificate: set the address for your devices in Settings → General.</div></div>}
    </div>
  )
}
