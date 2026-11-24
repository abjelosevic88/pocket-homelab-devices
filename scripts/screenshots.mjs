// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds three clocks that check in live):
//
//   DATA_DIR=/tmp/devices-demo DEMO_DATA=true ADMIN_EMAIL=demo@example.com ADMIN_PASSWORD=demodemo1 ADMIN_NAME=Maya PORT=18282 BASE_URL=https://devices.example.com ./pocket-devices
//   CDP=http://127.0.0.1:19271 BASE=http://127.0.0.1:18282 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                  # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18282'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false, scale = 0 } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: scale || (phone ? 3 : 2), isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'demo@example.com', password: 'demodemo1' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1500) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  // headless Chrome freezes CSS fade-ins: show everything at once
  await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name, full = false) { if (shots) await page.screenshot({ path: dir + name + '.png', fullPage: full }) }
async function deviceId(page, name) {
  const list = await page.evaluate(() => fetch('/api/v1/devices').then(r => r.json()))
  const d = list.find(x => x.name === name)
  assert.ok(d, `demo device ${name}`)
  return d.id
}

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/')
  assert.ok((await page.content()).includes('Bedroom clock'), 'the list shows the demo clocks')
  await shot(page, 'desktop-home')
  const bed = await deviceId(page, 'Bedroom clock')
  await go(page, `/devices/${bed}`, 2500)
  assert.ok((await page.content()).includes('Outdoor temperature'), 'the overview shows the status')
  await shot(page, 'desktop-device')
  await go(page, `/devices/${bed}?tab=commands`)
  await shot(page, 'desktop-commands')
  await go(page, `/devices/${bed}?tab=history`)
  await shot(page, 'desktop-history')
  await go(page, '/')
  await page.getByRole('button', { name: 'Add a device' }).first().click()
  await page.getByLabel('Name').fill('Living room clock')
  await page.getByLabel('WiFi MAC address').fill('24:6F:28:A1:B2:C3')
  await page.getByRole('button', { name: 'Add and get the token' }).click()
  await page.waitForTimeout(800)
  assert.ok((await page.content()).includes('cfg set device_token'), 'adding shows the console lines')
  await shot(page, 'desktop-add')
  await go(page, '/settings/general')
  await shot(page, 'desktop-settings-page')
  await ctx.close()
}

// ---- a setting being changed (1.5×: headless Chrome cannot capture the sticky save bar over a tall page at 2×) ----
{
  const { ctx, page } = await session({ scale: 1.5 })
  const bed = await deviceId(page, 'Bedroom clock')
  await go(page, `/devices/${bed}?tab=settings`)
  assert.ok((await page.content()).includes('Wake-up time'), 'the settings show the alarm')
  // a change waiting to be saved
  await page.getByLabel('Brightness', { exact: true }).fill('55')
  await page.evaluate(() => { const h = [...document.querySelectorAll('h3')].find(x => x.textContent === 'Display'); h?.scrollIntoView() })
  await page.waitForTimeout(300)
  await shot(page, 'desktop-settings')
  await page.getByRole('button', { name: 'Discard' }).click()
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/')
  await shot(page, 'phone-home')
  const bed = await deviceId(page, 'Bedroom clock')
  await go(page, `/devices/${bed}`, 2500)
  await shot(page, 'phone-device')
  await go(page, `/devices/${bed}?tab=settings`)
  await page.evaluate(() => { const h = [...document.querySelectorAll('h3')].find(x => x.textContent === 'Alarm'); h?.scrollIntoView() })
  await page.waitForTimeout(400)
  await shot(page, 'phone-settings')
  await ctx.close()
}

// ---- dark ----
{
  const { ctx, page } = await session({ dark: true })
  const kit = await deviceId(page, 'Kitchen clock')
  await go(page, `/devices/${kit}`, 2500)
  await shot(page, 'desktop-dark')
  await ctx.close()
}

await browser.close()
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log(shots ? 'screenshots written to docs/screenshots' : 'browser checks passed')
