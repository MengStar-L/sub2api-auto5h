import http from 'node:http'

const now = () => Math.floor(Date.now() / 1000)
let activated = false
let activatedAt = 0
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
  if (request.url === '/api/v1/admin/accounts/7/test') {
    const body = await readJSON(request)
    if (!body.prompt?.includes('最少取出多少个糖果') || !body.prompt?.includes('只能返回一个阿拉伯数字') || body.mode !== 'default' || body.model_id !== 'gpt-text-e2e') {
      return send(response, null, 400)
    }
    activated = true
    activatedAt = now()
    response.writeHead(200, { 'content-type': 'text/event-stream' })
    response.end('data: {"type":"content","text":"2"}\n\ndata: {"type":"content","text":"1"}\n\ndata: {"type":"test_complete","success":true}\n\n')
    return
  }
  send(response, null, 404)
})

server.listen(18081, '127.0.0.1')
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.close(() => process.exit(0)))
