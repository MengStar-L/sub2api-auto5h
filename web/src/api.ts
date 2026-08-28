interface Envelope<T> { data: T }
interface ErrorEnvelope { error?: { code?: string; message?: string } }

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

function cookie(name: string): string {
  const encoded = `${encodeURIComponent(name)}=`
  return document.cookie.split(';').map((item) => item.trim()).find((item) => item.startsWith(encoded))?.slice(encoded.length) ?? ''
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? 'GET').toUpperCase()
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD'].includes(method)) headers.set('X-CSRF-Token', decodeURIComponent(cookie('sub2api_auto5h_csrf')))
  const response = await fetch(path, { ...init, method, headers, credentials: 'same-origin' })
  const payload = await response.json().catch(() => ({})) as Envelope<T> & ErrorEnvelope
  if (!response.ok) throw new APIError(response.status, payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? `请求失败 (${response.status})`)
  return payload.data
}

export const json = (value: unknown) => JSON.stringify(value)
