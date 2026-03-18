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
}

export async function getInfo(): Promise<ServerInfo> {
  const res = await fetch('/api/info')
  return res.json()
}

export async function listSessions(): Promise<Session[]> {
  const res = await fetch('/api/sessions')
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

export async function killSession(id: string): Promise<void> {
  await fetch(`/api/sessions/${id}`, { method: 'DELETE' })
}
