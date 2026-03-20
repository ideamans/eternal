import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { connectSession, type SessionConnection } from '../lib/ws'
import { type AggregatedSession } from '../lib/api'
import { bspLayout } from '../lib/bsp'

const TILE_HEADER_HEIGHT = 28
const TILE_GAP = 2

interface TileState {
  session: AggregatedSession
  el: HTMLElement
  termViewport: HTMLElement
  termContainer: HTMLElement
  term: Terminal
  ws: SessionConnection
}

export function renderTiledView(
  mount: HTMLElement,
  sessions: AggregatedSession[],
  opts: {
    hasPeers: boolean
    onSelectSession: (session: AggregatedSession) => void
  },
): () => void {
  if (sessions.length === 0) {
    mount.innerHTML =
      '<div class="flex-1 flex items-center justify-center text-gray-500 text-sm">No active sessions</div>'
    return () => {}
  }

  mount.innerHTML = '<div id="tiles-viewport" style="position:relative;width:100%;height:100%;overflow:hidden;"></div>'
  const viewport = mount.querySelector('#tiles-viewport') as HTMLElement

  const tiles: TileState[] = []

  // Create all tiles
  for (const session of sessions) {
    const el = document.createElement('div')
    el.style.position = 'absolute'
    el.style.overflow = 'hidden'
    el.style.display = 'flex'
    el.style.flexDirection = 'column'
    el.className = 'border border-gray-700'
    viewport.appendChild(el)

    // Header
    const header = document.createElement('div')
    header.className =
      'flex items-center gap-2 px-2 bg-gray-800 border-b border-gray-700 cursor-pointer hover:bg-gray-700/60 flex-shrink-0'
    header.style.height = `${TILE_HEADER_HEIGHT}px`

    const cmd = session.command.join(' ')
    const shortCmd = cmd.length > 30 ? cmd.substring(0, 30) + '...' : cmd
    const dir = dirBasename(session.dir)
    const label = opts.hasPeers ? `${session.serverHostname}:${dir}` : dir

    header.innerHTML = `
      <span class="text-[10px] text-gray-500 truncate">${escapeHtml(label)}</span>
      <span class="text-xs font-mono text-gray-300 truncate flex-1">${escapeHtml(shortCmd)}</span>
      <span class="tile-status text-[10px] text-yellow-400 flex-shrink-0">...</span>
    `
    header.addEventListener('click', () => opts.onSelectSession(session))
    el.appendChild(header)

    // Terminal viewport (fills remaining space)
    const termViewport = document.createElement('div')
    termViewport.style.flex = '1'
    termViewport.style.overflow = 'hidden'
    termViewport.style.display = 'flex'
    termViewport.style.alignItems = 'flex-start'
    termViewport.style.justifyContent = 'flex-start'
    termViewport.className = 'bg-gray-950'
    el.appendChild(termViewport)

    const termContainer = document.createElement('div')
    termContainer.className = 'origin-top-left'
    termViewport.appendChild(termContainer)

    const term = new Terminal({
      cursorBlink: false,
      fontSize: 14,
      fontFamily: '"Menlo", "DejaVu Sans Mono", "Consolas", "Lucida Console", monospace',
      theme: {
        background: '#030712',
        foreground: '#e5e7eb',
        cursor: '#f59e0b',
      },
      disableStdin: true,
      scrollback: 0,
    })
    term.open(termContainer)

    // Hide xterm scrollbar in tile view
    const xtermViewport = termContainer.querySelector('.xterm-viewport') as HTMLElement
    if (xtermViewport) xtermViewport.style.overflow = 'hidden'

    const statusEl = header.querySelector('.tile-status') as HTMLElement

    const tile: TileState = { session, el, termViewport, termContainer, term, ws: null as any }
    const tileIndex = tiles.length
    tiles.push(tile)

    tile.ws = connectSession(
      session.id,
      {
        onOutput(data) {
          term.write(data)
        },
        onExit(exitCode) {
          statusEl.textContent = exitCode >= 0 ? `exit(${exitCode})` : 'lost'
          statusEl.className = 'tile-status text-[10px] text-red-400 flex-shrink-0'
        },
        onOpen() {
          statusEl.textContent = ''
          statusEl.className = 'tile-status text-[10px] text-green-400 flex-shrink-0'
        },
        onResize(cols, rows) {
          term.resize(cols, rows)
          updateTileScale(tileIndex)
        },
      },
      session.peerProxy,
    )
  }

  function layout() {
    const rect = { x: 0, y: 0, width: viewport.clientWidth, height: viewport.clientHeight }
    const items = bspLayout(sessions, rect)

    for (let i = 0; i < items.length; i++) {
      const { rect: r } = items[i]
      const tile = tiles[i]

      tile.el.style.left = `${r.x + TILE_GAP / 2}px`
      tile.el.style.top = `${r.y + TILE_GAP / 2}px`
      tile.el.style.width = `${r.width - TILE_GAP}px`
      tile.el.style.height = `${r.height - TILE_GAP}px`

      updateTileScale(i)
    }
  }

  function updateTileScale(index: number) {
    const tile = tiles[index]
    const container = tile.termContainer
    const vp = tile.termViewport

    container.style.transform = ''
    container.style.width = ''
    container.style.height = ''

    const termEl = container.querySelector('.xterm-screen') as HTMLElement
    if (!termEl) return

    const naturalW = termEl.offsetWidth
    const naturalH = termEl.offsetHeight
    const viewW = vp.clientWidth
    const viewH = vp.clientHeight

    if (naturalW <= 0 || naturalH <= 0) return

    const scaleX = viewW / naturalW
    const scaleY = viewH / naturalH
    const scale = Math.min(scaleX, scaleY, 1)

    container.style.transform = `scale(${scale})`
    container.style.width = `${naturalW}px`
    container.style.height = `${naturalH}px`
  }

  // Initial layout after terminals are rendered
  requestAnimationFrame(() => {
    layout()
  })

  const resizeObserver = new ResizeObserver(() => {
    layout()
  })
  resizeObserver.observe(viewport)

  return () => {
    resizeObserver.disconnect()
    for (const tile of tiles) {
      tile.ws.close()
      tile.term.dispose()
    }
  }
}

function dirBasename(dir: string): string {
  if (!dir) return '(unknown)'
  const parts = dir.replace(/\/+$/, '').split('/')
  return parts[parts.length - 1] || '/'
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}
