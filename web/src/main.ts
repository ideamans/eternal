import { listAllSessions, killSession, getInfo, type AggregatedSession } from './lib/api'
import { renderTerminal } from './pages/terminal'
import { renderTiledView } from './pages/tiled'

const app = document.getElementById('app')!

let cleanup: (() => void) | undefined
let refreshTimer: ReturnType<typeof setInterval> | undefined

/** Cached sessions for tiled view. */
let cachedSessions: AggregatedSession[] = []

/** Whether we have any peer servers configured. */
let hasPeers = false

/** Cached server info. */
let serverHostname = ''
let serverVersion = ''

function sessionKey(peerProxy: string, sessionId: string): string {
  return peerProxy ? `${peerProxy}|${sessionId}` : sessionId
}

function parseSessionKey(key: string): { peerProxy: string; sessionId: string } {
  const sep = key.lastIndexOf('|')
  if (sep === -1) return { peerProxy: '', sessionId: key }
  return { peerProxy: key.substring(0, sep), sessionId: key.substring(sep + 1) }
}

function init() {
  getInfo('/api').then((info) => {
    serverHostname = info.hostname
    serverVersion = info.version || ''
  })

  // Global keyboard shortcut: Alt+T to toggle tiled view
  document.addEventListener('keydown', (e) => {
    if (e.altKey && e.code === 'KeyT') {
      e.preventDefault()
      if (location.hash === '#/tiles') {
        location.hash = '#/'
      } else {
        location.hash = '#/tiles'
      }
    }
  })

  window.addEventListener('hashchange', onRoute)
  onRoute()
}

function onRoute() {
  const hash = location.hash || '#/'

  // Cleanup previous view
  cleanup?.()
  cleanup = undefined
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = undefined
  }

  if (hash === '#/tiles') {
    renderTiledMode()
  } else {
    renderNormalMode()
  }
}

// ─── Normal mode (sidebar + single session) ────────────────────────

function renderNormalMode() {
  app.innerHTML = `
    <div class="flex h-screen">
      <aside id="sidebar" class="w-64 flex-shrink-0 bg-gray-800 border-r border-gray-700 flex flex-col">
        <div class="px-4 py-3 border-b border-gray-700">
          <div class="flex items-center gap-2">
            <h1 class="text-sm font-bold text-gray-200 tracking-wide">ETERNAL</h1>
            <span id="hostname" class="text-xs text-gray-500 font-mono">${escapeHtml(serverHostname)}</span>
            <span id="version" class="text-[10px] text-gray-600 font-mono">${serverVersion ? 'v' + escapeHtml(serverVersion) : ''}</span>
            <span class="flex-1"></span>
            <button id="btn-tiles" class="text-gray-500 hover:text-gray-200 cursor-pointer" title="Tile view (Alt+T)">
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></svg>
            </button>
          </div>
        </div>
        <div id="session-list" class="flex-1 overflow-y-auto"></div>
      </aside>
      <main id="main-content" class="flex-1 flex flex-col min-w-0">
        <div class="flex-1 flex items-center justify-center text-gray-500 text-sm">
          Select a session from the sidebar
        </div>
      </main>
    </div>
  `

  document.getElementById('btn-tiles')!.addEventListener('click', () => {
    location.hash = '#/tiles'
  })

  refreshSessionList()
  refreshTimer = setInterval(refreshSessionList, 3000)

  // Render the active session if any
  const hash = location.hash || '#/'
  const match = hash.match(/^#\/session\/(.+)$/)
  if (match) {
    const key = decodeURIComponent(match[1])
    const { peerProxy, sessionId } = parseSessionKey(key)
    const mainContent = document.getElementById('main-content')!
    mainContent.innerHTML = '<div id="terminal-mount" class="flex-1 flex flex-col"></div>'
    const mount = document.getElementById('terminal-mount')!
    cleanup = renderTerminal(mount, sessionId, peerProxy)
    highlightActiveSession(key)
  }
}

async function refreshSessionList() {
  const listEl = document.getElementById('session-list')
  if (!listEl) return

  try {
    cachedSessions = await listAllSessions()
  } catch {
    return
  }

  hasPeers = cachedSessions.some((s) => s.peerProxy !== '')

  if (cachedSessions.length === 0) {
    listEl.innerHTML = `
      <div class="px-4 py-8 text-center text-gray-500 text-xs">
        No active sessions
      </div>
    `
    return
  }

  // Group by (hostname + dir basename) when peers exist, otherwise just dir basename
  const groups = new Map<string, AggregatedSession[]>()
  for (const s of cachedSessions) {
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
        ${items.map((s) => sessionItem(s, sessionKey(s.peerProxy, s.id) === activeKey)).join('')}
      </div>
    `
  }

  listEl.innerHTML = html

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
      const { peerProxy, sessionId } = parseSessionKey(key)
      await killSession(sessionId, peerProxy)
      if (getActiveSessionKey() === key) {
        location.hash = '#/'
      }
      refreshSessionList()
    })
  })
}

function sessionItem(s: AggregatedSession, active: boolean): string {
  const key = sessionKey(s.peerProxy, s.id)
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

// ─── Tiled mode ─────────────────────────────────────────────────────

async function renderTiledMode() {
  app.innerHTML = `
    <div class="flex flex-col h-screen">
      <header class="flex items-center gap-3 px-4 py-2 bg-gray-800 border-b border-gray-700 flex-shrink-0">
        <button id="btn-back" class="text-gray-400 hover:text-gray-200 cursor-pointer" title="List view (Alt+T)">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
        </button>
        <h1 class="text-sm font-bold text-gray-200 tracking-wide">ETERNAL</h1>
        <span class="text-xs text-gray-500 font-mono">${escapeHtml(serverHostname)}</span>
        <span class="text-[10px] text-gray-600 font-mono">${serverVersion ? 'v' + escapeHtml(serverVersion) : ''}</span>
        <span class="flex-1"></span>
        <span class="text-[10px] text-gray-600">Alt+T to toggle</span>
      </header>
      <div id="tiles-mount" class="flex-1 overflow-hidden"></div>
    </div>
  `

  document.getElementById('btn-back')!.addEventListener('click', () => {
    location.hash = '#/'
  })

  const mount = document.getElementById('tiles-mount')!

  let tileCleanup: (() => void) | undefined

  function currentSessionKeys(sessions: AggregatedSession[]): string {
    return sessions.map((s) => sessionKey(s.peerProxy, s.id)).sort().join(',')
  }

  async function refreshTiles() {
    let sessions: AggregatedSession[]
    try {
      sessions = await listAllSessions()
    } catch {
      return
    }

    hasPeers = sessions.some((s) => s.peerProxy !== '')

    // Check if session list changed
    const newKeys = currentSessionKeys(sessions)
    const oldKeys = currentSessionKeys(cachedSessions)
    cachedSessions = sessions

    if (tileCleanup && newKeys === oldKeys) return

    // Rebuild tiles
    tileCleanup?.()
    tileCleanup = undefined

    if (sessions.length === 0) {
      mount.innerHTML =
        '<div class="flex-1 flex items-center justify-center text-gray-500 text-sm">No active sessions</div>'
      return
    }

    mount.innerHTML = ''
    tileCleanup = renderTiledView(mount, sessions, {
      hasPeers,
      onSelectSession(session) {
        const key = sessionKey(session.peerProxy, session.id)
        location.hash = `#/session/${encodeURIComponent(key)}`
      },
    })
  }

  await refreshTiles()
  refreshTimer = setInterval(refreshTiles, 3000)

  cleanup = () => {
    tileCleanup?.()
  }
}

// ─── Utilities ──────────────────────────────────────────────────────

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
