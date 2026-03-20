export interface Session {
  id: string
  name: string
  command: string[]
  dir: string
  cols: number
  rows: number
  clients: number
  created_at: string
  last_used: string
}

export interface ServerInfo {
  hostname: string
  version: string
}

export interface AggregatedSession extends Session {
  /** Proxy base path for this session's server. Empty string means local. */
  peerProxy: string
  /** Hostname of the server this session belongs to. */
  serverHostname: string
}

export async function getInfo(basePath = '/api'): Promise<ServerInfo> {
  const res = await fetch(`${basePath}/info`)
  return res.json()
}

export async function listSessions(basePath = '/api'): Promise<Session[]> {
  const res = await fetch(`${basePath}/sessions`)
  return res.json()
}

export async function createSession(opts: {
  name?: string
  command: string[]
  cols: number
  rows: number
}): Promise<Session> {
  const res = await fetch('/api/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(opts),
  })
  return res.json()
}

export async function killSession(id: string, basePath = '/api'): Promise<void> {
  await fetch(`${basePath}/sessions/${id}`, { method: 'DELETE' })
}

export async function getPeers(): Promise<string[]> {
  const res = await fetch('/api/peers')
  return res.json()
}

/**
 * Fetch sessions from the local server and all peers, returning aggregated results.
 * All requests go through the local server (peers are proxied via /api/peer/{index}/).
 */
export async function listAllSessions(): Promise<AggregatedSession[]> {
  const [localInfo, localSessions, peers] = await Promise.all([
    getInfo('/api'),
    listSessions('/api'),
    getPeers(),
  ])

  const result: AggregatedSession[] = localSessions.map((s) => ({
    ...s,
    peerProxy: '',
    serverHostname: localInfo.hostname,
  }))

  if (peers.length > 0) {
    const peerResults = await Promise.allSettled(
      peers.map(async (_peerUrl, index) => {
        const basePath = `/api/peer/${index}`
        const [info, sessions] = await Promise.all([getInfo(basePath), listSessions(basePath)])
        return sessions.map((s) => ({
          ...s,
          peerProxy: basePath,
          serverHostname: info.hostname,
        }))
      }),
    )

    for (const r of peerResults) {
      if (r.status === 'fulfilled') {
        result.push(...r.value)
      }
    }
  }

  return result
}
