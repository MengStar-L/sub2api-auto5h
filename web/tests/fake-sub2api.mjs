import http from 'node:http'

const now = () => Math.floor(Date.now() / 1000)
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

const server = http.createServer((request, response) => {
  if (request.url === '/health') return send(response, { status: 'ok' })
  if (request.headers['x-api-key'] !== 'e2e-admin-key') return send(response, null, 401)
  if (request.url === '/api/v1/admin/system/version') return send(response, { version: '0.1.183' })
  if (request.url?.startsWith('/api/v1/admin/accounts?')) return send(response, { items: [account], total: 1 })
  if (request.url === '/api/v1/admin/openai/accounts/7/quota') {
    return send(response, {
      plan_type: 'plus', account_id: 'workspace-plus-7', fetched_at: now(),
      rate_limit: {
        allowed: true, limit_reached: false,
        primary_window: { used_percent: 12, limit_window_seconds: 18000, reset_after_seconds: 17900, reset_at: now() + 17900 },
        secondary_window: { used_percent: 5, limit_window_seconds: 604800, reset_after_seconds: 500000, reset_at: now() + 500000 },
      },
    })
  }
  if (request.url === '/api/v1/admin/accounts/7/models') return send(response, [{ id: 'gpt-text-e2e' }])
  if (request.url === '/api/v1/admin/accounts/7/test') {
    response.writeHead(200, { 'content-type': 'text/event-stream' })
    response.end('data: {"type":"test_complete","success":true}\n\n')
    return
  }
  send(response, null, 404)
})

server.listen(18081, '127.0.0.1')
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.close(() => process.exit(0)))
