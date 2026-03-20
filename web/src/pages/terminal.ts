import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { connectSession } from '../lib/ws'

export function renderTerminal(mount: HTMLElement, sessionId: string, peerProxy = '') {
  mount.innerHTML = `
    <div class="flex items-center justify-between px-4 py-2 bg-gray-800 border-b border-gray-700">
      <span class="text-gray-400 text-xs font-mono">${sessionId.substring(0, 8)}</span>
      <div id="status" class="text-xs text-yellow-400">connecting...</div>
    </div>
    <div id="terminal-viewport" class="flex-1 overflow-hidden flex items-start justify-start bg-gray-950">
      <div id="terminal-container" class="origin-top-left"></div>
    </div>
  `

  const viewport = mount.querySelector('#terminal-viewport') as HTMLElement
  const container = mount.querySelector('#terminal-container') as HTMLElement
  const statusEl = mount.querySelector('#status') as HTMLElement

  const term = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: '"Hack Nerd Font Mono", "Menlo", "DejaVu Sans Mono", "Consolas", "Lucida Console", monospace',
    theme: {
      background: '#030712',
      foreground: '#e5e7eb',
      cursor: '#f59e0b',
    },
  })

  term.open(container)

  function updateScale() {
    container.style.transform = ''
    container.style.width = ''
    container.style.height = ''

    const termEl = container.querySelector('.xterm-screen') as HTMLElement
    if (!termEl) return

    const naturalW = termEl.offsetWidth
    const naturalH = termEl.offsetHeight
    const viewW = viewport.clientWidth
    const viewH = viewport.clientHeight

    if (naturalW <= 0 || naturalH <= 0) return

    const scaleX = viewW / naturalW
    const scaleY = viewH / naturalH
    const scale = Math.min(scaleX, scaleY, 1)

    container.style.transform = `scale(${scale})`
    container.style.width = `${naturalW}px`
    container.style.height = `${naturalH}px`
  }

  const ws = connectSession(
    sessionId,
    {
      onOutput(data) {
        term.write(data)
      },
      onExit(exitCode) {
        statusEl.textContent = exitCode >= 0 ? `exited (${exitCode})` : 'disconnected'
        statusEl.className = 'text-xs text-red-400'
        term.write('\r\n\x1b[90m--- session ended ---\x1b[0m\r\n')
      },
      onOpen() {
        statusEl.textContent = 'connected'
        statusEl.className = 'text-xs text-green-400'
      },
      onResize(cols, rows) {
        term.resize(cols, rows)
        updateScale()
      },
    },
    peerProxy,
  )

  term.onData((data) => {
    ws.send(data)
  })

  const resizeObserver = new ResizeObserver(() => {
    updateScale()
  })
  resizeObserver.observe(viewport)

  requestAnimationFrame(updateScale)

  return () => {
    resizeObserver.disconnect()
    ws.close()
    term.dispose()
  }
}
