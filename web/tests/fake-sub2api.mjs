import http from 'node:http'

const now = () => Math.floor(Date.now() / 1000)
let activated = false
let activatedAt = 0
let exportCount = 0
let refreshCount = 0
let codexRequestCount = 0
const account = {
  id: 7,
  name: 'plus@example.com',
  platform: 'openai',
  type: 'oauth',
  credentials: { email: 'plus@example.com', plan_type: 'plus', chatgpt_account_id: 'workspace-plus-7' },
  extra: {},
  status: 'active',
  schedulable: true,
  parent_account_id: null,
  created_at: '2026-08-27T00:00:00Z',
}

function send(response, data, status = 200) {
  response.writeHead(status, { 'content-type': 'application/json' })
  response.end(JSON.stringify({ code: status === 200 ? 0 : status, message: status === 200 ? 'success' : 'error', data }))
}

async function readJSON(request) {
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  return JSON.parse(Buffer.concat(chunks).toString('utf8'))
}

const server = http.createServer(async (request, response) => {
  if (request.url === '/health') return send(response, { status: 'ok' })
  if (request.url === '/test-status') return send(response, { export_count: exportCount, refresh_count: refreshCount, codex_request_count: codexRequestCount })
  if (request.headers['x-api-key'] !== 'e2e-admin-key') return send(response, null, 401)
  if (request.url === '/api/v1/admin/system/version') return send(response, { version: '0.1.183' })
  if (request.url?.startsWith('/api/v1/admin/accounts?')) return send(response, { items: [account], total: 1 })
  if (request.url === '/api/v1/admin/openai/accounts/7/quota') {
    const fetchedAt = now()
    const fiveResetAt = activated ? activatedAt + 17900 : fetchedAt + 18000
    return send(response, {
      plan_type: 'plus', account_id: 'workspace-plus-7', fetched_at: fetchedAt,
      rate_limit: {
        allowed: true, limit_reached: false,
        primary_window: { used_percent: activated ? 1 : 0, limit_window_seconds: 18000, reset_after_seconds: activated ? Math.max(0, fiveResetAt - fetchedAt) : 18000, reset_at: fiveResetAt },
        secondary_window: { used_percent: 5, limit_window_seconds: 604800, reset_after_seconds: 500000, reset_at: fetchedAt + 500000 },
      },
    })
  }
  if (request.url === '/api/v1/admin/accounts/7/models') return send(response, [{ id: 'gpt-text-e2e' }])
  if (request.url === '/api/v1/admin/accounts/data?ids=7&include_proxies=true') {
    exportCount++
    return send(response, {
      accounts: [{
        platform: 'openai', type: 'oauth', proxy_key: null, extra: {},
        credentials: {
          access_token: 'e2e-access-token', chatgpt_account_id: 'workspace-plus-7',
          email: 'plus@example.com', expires_at: new Date(Date.now() + 3600_000).toISOString(),
        },
      }],
      proxies: [],
    })
  }
  if (request.url === '/api/v1/admin/openai/accounts/7/refresh' && request.method === 'POST') {
    refreshCount++
    return send(response, {})
  }
  send(response, null, 404)
})

const codexServer = http.createServer(async (request, response) => {
  if (request.url === '/health') {
    response.writeHead(200, { 'content-type': 'application/json' })
    response.end('{"status":"ok"}')
    return
  }
  if (request.url === '/backend-api/codex/responses' && request.method === 'POST') {
    codexRequestCount++
    const body = await readJSON(request)
    const prompt = body.input?.[0]?.content?.[0]?.text
    if (request.headers.authorization !== 'Bearer e2e-access-token' || request.headers['chatgpt-account-id'] !== 'workspace-plus-7' ||
        !prompt?.includes('最少取出多少个糖果') || !body.instructions?.includes('只能返回一个阿拉伯数字') ||
        body.model !== 'gpt-text-e2e' || body.store !== false || body.stream !== true) {
      response.writeHead(400, { 'content-type': 'application/json' })
      response.end('{"error":{"code":"bad_fixture_request","message":"invalid request"}}')
      return
    }
    activated = true
    activatedAt = now()
    response.writeHead(200, {
      'content-type': 'text/event-stream',
      'x-codex-primary-window-minutes': '300',
      'x-codex-primary-used-percent': '1',
      'x-codex-primary-reset-after-seconds': '17900',
      'x-codex-secondary-window-minutes': '10080',
      'x-codex-secondary-used-percent': '5',
      'x-codex-secondary-reset-after-seconds': '500000',
    })
    response.end('data: {"type":"response.output_text.delta","delta":"2"}\n\ndata: {"type":"response.output_text.done","text":"21"}\n\ndata: {"type":"response.completed","response":{"output":[]}}\n\n')
    return
  }
  response.writeHead(404, { 'content-type': 'application/json' })
  response.end('{"error":{"code":"not_found","message":"not found"}}')
})

server.listen(18081, '127.0.0.1')
codexServer.listen(18082, '127.0.0.1')
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => {
  server.close()
  codexServer.close(() => process.exit(0))
})
