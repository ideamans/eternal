import { listAllSessions, killSession, getInfo, type AggregatedSession } from './lib/api'
import { renderTerminal } from './pages/terminal'

const app = document.getElementById('app')!

let cleanup: (() => void) | undefined
let refreshTimer: ReturnType<typeof setInterval> | undefined

/** Map from composite key "serverBase:sessionId" to AggregatedSession for routing. */
let sessionIndex = new Map<string, AggregatedSession>()

/** Whether we have any peer servers configured. */
let hasPeers = false

function sessionKey(serverBase: string, sessionId: string): string {
  return serverBase ? `${serverBase}|${sessionId}` : sessionId
}

function parseSessionKey(key: string): { serverBase: string; sessionId: string } {
  const sep = key.lastIndexOf('|')
  if (sep === -1) return { serverBase: '', sessionId: key }
  return { serverBase: key.substring(0, sep), sessionId: key.substring(sep + 1) }
}

function init() {
  app.innerHTML = `
    <div class="flex h-screen">
      <aside id="sidebar" class="w-64 flex-shrink-0 bg-gray-800 border-r border-gray-700 flex flex-col">
        <div class="px-4 py-3 border-b border-gray-700">
          <div class="flex items-center gap-2">
            <h1 class="text-sm font-bold text-gray-200 tracking-wide">ETERNAL</h1>
            <span id="hostname" class="text-xs text-gray-500 font-mono"></span>
            <span id="version" class="text-[10px] text-gray-600 font-mono"></span>
          </div>
        </div>
        <div id="session-list" class="flex-1 overflow-y-auto"></div>
      </aside>
      <main id="main-content" class="flex-1 flex flex-col min-w-0">
        <div id="welcome" class="flex-1 flex items-center justify-center text-gray-500 text-sm">
          Select a session from the sidebar
        </div>
      </main>
    </div>
  `

  getInfo().then((info) => {
    const el = document.getElementById('hostname')
    if (el) el.textContent = info.hostname
    const ver = document.getElementById('version')
    if (ver && info.version) ver.textContent = `v${info.version}`
  })

  refreshSessionList()
  refreshTimer = setInterval(refreshSessionList, 3000)

  window.addEventListener('hashchange', onRoute)
  onRoute()
}

function onRoute() {
  const hash = location.hash || '#/'
  const match = hash.match(/^#\/session\/(.+)$/)

  cleanup?.()
  cleanup = undefined

  const mainContent = document.getElementById('main-content')!

  if (match) {
    const key = decodeURIComponent(match[1])
    const { serverBase, sessionId } = parseSessionKey(key)
    mainContent.innerHTML = '<div id="terminal-mount" class="flex-1 flex flex-col"></div>'
    const mount = document.getElementById('terminal-mount')!
    cleanup = renderTerminal(mount, sessionId, serverBase)
    highlightActiveSession(key)
  } else {
    mainContent.innerHTML = `
      <div class="flex-1 flex items-center justify-center text-gray-500 text-sm">
        Select a session from the sidebar
      </div>
    `
    highlightActiveSession(null)
  }
}

async function refreshSessionList() {
  const listEl = document.getElementById('session-list')
  if (!listEl) return

  let sessions: AggregatedSession[]
  try {
    sessions = await listAllSessions()
  } catch {
    return
  }

  // Track whether we have peers for display logic
  hasPeers = sessions.some((s) => s.serverBase !== '')

  // Rebuild session index
  sessionIndex = new Map()
  for (const s of sessions) {
    sessionIndex.set(sessionKey(s.serverBase, s.id), s)
  }

  if (sessions.length === 0) {
    listEl.innerHTML = `
      <div class="px-4 py-8 text-center text-gray-500 text-xs">
        No active sessions
      </div>
    `
    return
  }

  // Group by (hostname + dir basename) when peers exist, otherwise just dir basename
  const groups = new Map<string, AggregatedSession[]>()
  for (const s of sessions) {
    const dir = dirBasename(s.dir)
    const groupName = hasPeers ? `${s.serverHostname}:${dir}` : dir
    if (!groups.has(groupName)) groups.set(groupName, [])
    groups.get(groupName)!.push(s)
  }

  const activeKey = getActiveSessionKey()

  let html = ''
  for (const [groupName, items] of groups) {
    html += `
      <div class="mt-2">
        <div class="px-4 py-1 text-[10px] font-medium text-gray-500 uppercase tracking-wider">${escapeHtml(groupName)}</div>
        ${items.map((s) => sessionItem(s, sessionKey(s.serverBase, s.id) === activeKey)).join('')}
      </div>
    `
  }

  listEl.innerHTML = html

  // Attach event listeners
  listEl.querySelectorAll('[data-session-key]').forEach((el) => {
    el.addEventListener('click', (e) => {
      const target = e.target as HTMLElement
      if (target.closest('[data-kill]')) return
      const key = (el as HTMLElement).dataset.sessionKey!
      location.hash = `#/session/${encodeURIComponent(key)}`
    })
  })

  listEl.querySelectorAll('[data-kill]').forEach((btn) => {
    btn.addEventListener('click', async (e) => {
      e.stopPropagation()
      const key = (btn as HTMLElement).dataset.kill!
      const { serverBase, sessionId } = parseSessionKey(key)
      await killSession(sessionId, serverBase)
      if (getActiveSessionKey() === key) {
        location.hash = '#/'
      }
      refreshSessionList()
    })
  })
}

function sessionItem(s: AggregatedSession, active: boolean): string {
  const key = sessionKey(s.serverBase, s.id)
  const cmd = s.command.join(' ')
  const short = cmd.length > 20 ? cmd.substring(0, 20) + '...' : cmd
  const time = new Date(s.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const activeClass = active
    ? 'bg-gray-700/60 border-l-2 border-blue-400'
    : 'hover:bg-gray-700/40 border-l-2 border-transparent'

  return `
    <div data-session-key="${escapeHtml(key)}" class="group flex items-center gap-2 px-3 py-2 cursor-pointer ${activeClass}">
      <div class="flex-1 min-w-0">
        <div class="text-xs font-mono text-gray-200 truncate">${escapeHtml(short)}</div>
        <div class="flex items-center gap-2 mt-0.5">
          <span class="text-[10px] text-gray-500">${time}</span>
          ${s.clients > 0 ? `<span class="text-[10px] text-green-500">${s.clients} connected</span>` : ''}
        </div>
      </div>
      <button data-kill="${escapeHtml(key)}" class="opacity-0 group-hover:opacity-100 px-2 py-1 text-gray-400 hover:text-red-400 hover:bg-red-400/10 rounded text-sm leading-none cursor-pointer" title="Kill session">&times;</button>
    </div>
  `
}

function dirBasename(dir: string): string {
  if (!dir) return '(unknown)'
  const parts = dir.replace(/\/+$/, '').split('/')
  return parts[parts.length - 1] || '/'
}

function getActiveSessionKey(): string | null {
  const match = location.hash.match(/^#\/session\/(.+)$/)
  return match ? decodeURIComponent(match[1]) : null
}

function highlightActiveSession(key: string | null) {
  document.querySelectorAll('[data-session-key]').forEach((el) => {
    const elKey = (el as HTMLElement).dataset.sessionKey
    if (elKey === key) {
      el.classList.add('bg-gray-700/60', 'border-blue-400')
      el.classList.remove('border-transparent', 'hover:bg-gray-700/40')
    } else {
      el.classList.remove('bg-gray-700/60', 'border-blue-400')
      el.classList.add('border-transparent', 'hover:bg-gray-700/40')
    }
  })
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

init()
