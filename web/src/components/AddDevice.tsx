// Add a device: kind, name, hardware id, place — then the token, shown once,
// with the console lines for the device, or the fields of its setup page.
import React, { useState } from 'react'
import { api, V1 } from '../lib/api'
import { CATEGORY_LABEL, POWER_LABEL, kindIcon, useKinds } from '../lib/devices'
import type { DeviceDetail } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { Field, Loading, Modal, Segmented, useToast } from './ui'
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
        footer={<button type="button" className="btn primary" onClick={() => onAdded(made)}>Done</button>}>
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

type SetupWay = 'usb' | 'phone'
const WAY_KEY = 'pdev-setup-way'

/** The token (once) and the two ways to give it to the device: the lines
 * for its USB console, or the fields of its own setup page (the device's
 * WiFi network, opened from a phone). */
export function SetupSteps({ detail, onCopied }: { detail: DeviceDetail; onCopied: () => void }) {
  const lines = detail.setup || []
  const [way, setWay] = useState<SetupWay>(() => (localStorage.getItem(WAY_KEY) as SetupWay) || 'usb')
  const pick = (w: SetupWay) => { setWay(w); localStorage.setItem(WAY_KEY, w) }
  const copy = (t: string) => { navigator.clipboard?.writeText(t).then(onCopied).catch(() => {}) }
  const insecure = !detail.server_url.startsWith('https://')
  const copyBtn = (t: string, what: string) => <button type="button" className="btn ghost sm icon-only" aria-label={`Copy ${what}`} title={`Copy ${what}`} onClick={() => copy(t)}><Icon name="copy" size={14} /></button>
  return (
    <div className="stack">
      <Segmented label="How to set it up" value={way} onChange={pick} options={[
        { value: 'usb', label: 'USB cable' },
        { value: 'phone', label: 'Phone, setup page' },
      ]} />
      {way === 'usb' ? <>
        <p>{detail.setup_help}</p>
        <div className="console">
          {lines.map(l => (
            <div key={l} className="console-line"><code>{l}</code>{copyBtn(l, 'this line')}</div>
          ))}
        </div>
        <div className="row wrap"><button type="button" className="btn" onClick={() => copy(lines.join('\n'))}><Icon name="copy" size={15} />Copy all three lines</button></div>
      </> : <>
        <ol className="ap-steps">
          <li><b>Open its setup network.</b> {detail.ap_help}</li>
          <li><b>Join that WiFi network on your phone.</b> The setup page opens by itself; if it does not, open <code>http://{detail.ap_page || '192.168.4.1'}</code> in the browser.</li>
          <li><b>Fill in the four fields and save.</b> The device joins your WiFi and checks in here within a minute.</li>
        </ol>
        <dl className="ap-fields">
          <div><dt>WiFi name</dt><dd className="muted">your home WiFi</dd></div>
          <div><dt>WiFi password</dt><dd className="muted">its password</dd></div>
          <div><dt>Server address</dt><dd><code className="ap-value">{detail.server_url}</code>{copyBtn(detail.server_url, 'the server address')}</dd></div>
          {detail.token && <div><dt>Token</dt><dd><code className="ap-value ap-token">{detail.token}</code>{copyBtn(detail.token, 'the token')}</dd></div>}
        </dl>
        <p className="muted small">While your phone is on the device’s network it has no internet, but this page stays open: switch back to it to copy. Dashes and capital letters in the token do not matter.</p>
      </>}
      <div className="callout warn"><Icon name="key" size={15} /><div>The token is shown <b>only now</b>. If you lose it, make a new one on the device’s Connection tab; the old one stops working at once.</div></div>
      {insecure && <div className="callout danger"><Icon name="alert" size={15} /><div>The address is not https://. The device only talks to a server with a real certificate: set the address for your devices in Settings → General.</div></div>}
    </div>
  )
}
