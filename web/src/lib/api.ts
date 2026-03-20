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
  /** Base URL of the server this session belongs to. Empty string means local. */
  serverBase: string
  /** Hostname of the server this session belongs to. */
  serverHostname: string
}

export async function getInfo(baseUrl = ''): Promise<ServerInfo> {
  const res = await fetch(`${baseUrl}/api/info`)
  return res.json()
}

export async function listSessions(baseUrl = ''): Promise<Session[]> {
  const res = await fetch(`${baseUrl}/api/sessions`)
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

export async function killSession(id: string, baseUrl = ''): Promise<void> {
  await fetch(`${baseUrl}/api/sessions/${id}`, { method: 'DELETE' })
}

export async function getPeers(): Promise<string[]> {
  const res = await fetch('/api/peers')
  return res.json()
}

/**
 * Fetch sessions from the local server and all peers, returning aggregated results.
 */
export async function listAllSessions(): Promise<AggregatedSession[]> {
  const [localInfo, localSessions, peers] = await Promise.all([getInfo(), listSessions(), getPeers()])

  const result: AggregatedSession[] = localSessions.map((s) => ({
    ...s,
    serverBase: '',
    serverHostname: localInfo.hostname,
  }))

  if (peers.length > 0) {
    const peerResults = await Promise.allSettled(
      peers.map(async (peerUrl) => {
        const [info, sessions] = await Promise.all([getInfo(peerUrl), listSessions(peerUrl)])
        return sessions.map((s) => ({
          ...s,
          serverBase: peerUrl,
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
