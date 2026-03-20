interface Message {
  type: 'input' | 'output' | 'resize' | 'exit'
  data?: string
  cols?: number
  rows?: number
  exit_code?: number
}

export interface SessionConnection {
  send: (data: string) => void
  resize: (cols: number, rows: number) => void
  close: () => void
}

export interface ConnectOptions {
  onOutput: (data: Uint8Array) => void
  onExit: (exitCode: number) => void
  onOpen?: () => void
  onResize?: (cols: number, rows: number) => void
}

/**
 * Connect to a session via WebSocket.
 * @param sessionId - The session ID to connect to.
 * @param opts - Callbacks for output, exit, open, and resize events.
 * @param serverBase - Base URL of the server (empty string for local).
 */
export function connectSession(sessionId: string, opts: ConnectOptions, serverBase = ''): SessionConnection {
  let wsUrl: string

  if (serverBase) {
    // Peer server: derive WebSocket URL from HTTP base URL
    const url = new URL(serverBase)
    const wsProtocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    wsUrl = `${wsProtocol}//${url.host}/ws/session/${sessionId}`
  } else {
    // Local server: use current page host
    const wsProtocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    wsUrl = `${wsProtocol}//${location.host}/ws/session/${sessionId}`
  }

  const ws = new WebSocket(wsUrl)

  const queue: string[] = []
  let opened = false

  ws.onopen = () => {
    opened = true
    for (const msg of queue) {
      ws.send(msg)
    }
    queue.length = 0
    opts.onOpen?.()
  }

  ws.onmessage = (event) => {
    try {
      const msg: Message = JSON.parse(event.data)
      switch (msg.type) {
        case 'output':
          if (msg.data) {
            const binary = atob(msg.data)
            const bytes = new Uint8Array(binary.length)
            for (let i = 0; i < binary.length; i++) {
              bytes[i] = binary.charCodeAt(i)
            }
            opts.onOutput(bytes)
          }
          break
        case 'resize':
          if (msg.cols && msg.rows) {
            opts.onResize?.(msg.cols, msg.rows)
          }
          break
        case 'exit':
          opts.onExit(msg.exit_code ?? 0)
          break
      }
    } catch (e) {
      console.error('ws message parse error:', e)
    }
  }

  ws.onerror = (e) => {
    console.error('ws error:', e)
  }

  ws.onclose = () => {
    if (opened) {
      opts.onExit(-1)
    }
  }

  function sendRaw(json: string) {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(json)
    } else if (ws.readyState === WebSocket.CONNECTING) {
      queue.push(json)
    }
  }

  return {
    send(data: string) {
      const bytes = new TextEncoder().encode(data)
      let binary = ''
      for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i])
      }
      sendRaw(JSON.stringify({ type: 'input', data: btoa(binary) }))
    },
    resize(cols: number, rows: number) {
      sendRaw(JSON.stringify({ type: 'resize', cols, rows }))
    },
    close() {
      ws.close()
    },
  }
}
