import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Plugin } from 'vite'

type Account = Record<string, unknown>

const now = () => Math.floor(Date.now() / 1000)

function sampleAccounts(): Account[] {
  const t = now()
  return [
    {
      id: 'acc-plus',
      remote_id: 7,
      remote_created_at: '2026-08-12T08:00:00Z',
      identity_generation: 1,
      name: 'plus@example.com',
      email: 'plus@example.com',
      plan_type: 'plus',
      platform: 'openai',
      account_type: 'oauth',
      status: 'active',
      schedulable: true,
      parent_account_id: null,
      missing: false,
      eligible: true,
      eligibility_reason: '',
      five_reset_at: t + 9_240,
      five_used_percent: 38,
      seven_reset_at: t + 412_000,
      seven_used_percent: 12,
      quota_fetched_at: t - 40,
      quota_state: 'ok',
      next_action_at: t + 9_270,
      runtime_state: 'waiting',
      last_error: '',
      last_answer_status: 'normal',
      last_answer_text: '21',
      last_answer_at: t - 18_400,
      last_request_model: 'gpt-5.4',
      last_transport_path: 'official_codex_sse',
      last_answer_source: 'response.completed',
      last_quota_evidence: 'official_headers',
      last_terminal_summary: '已验证 · 5h 窗口已启动',
      policy: { enabled: true, enable_generation: 1, model_override: null, grace_override_seconds: null, max_retries_override: null, retry_base_override_seconds: null },
    },
    {
      id: 'acc-team',
      remote_id: 18,
      remote_created_at: '2026-07-03T02:11:00Z',
      identity_generation: 1,
      name: 'team-ops',
      email: 'team@example.com',
      plan_type: 'team',
      platform: 'openai',
      account_type: 'oauth',
      status: 'active',
      schedulable: true,
      parent_account_id: null,
      missing: false,
      eligible: true,
      eligibility_reason: '',
      five_reset_at: t + 1_120,
      five_used_percent: 91,
      seven_reset_at: t + 380_000,
      seven_used_percent: 44,
      quota_fetched_at: t - 12,
      quota_state: 'ok',
      next_action_at: t + 1_150,
      runtime_state: 'waiting',
      last_error: '',
      last_answer_status: 'abnormal',
      last_answer_text: '二十二',
      last_answer_at: t - 17_900,
      last_request_model: 'gpt-5.4',
      last_transport_path: 'official_codex_sse',
      last_answer_source: 'response.completed',
      last_quota_evidence: 'quota_poll',
      last_terminal_summary: '请求成功 · 答案非 21',
      policy: { enabled: true, enable_generation: 1, model_override: 'gpt-5.4', grace_override_seconds: 45, max_retries_override: null, retry_base_override_seconds: null },
    },
    {
      id: 'acc-biz',
      remote_id: 22,
      remote_created_at: '2026-06-19T16:40:00Z',
      identity_generation: 1,
      name: 'biz-workspace',
      email: 'biz@example.com',
      plan_type: 'business',
      platform: 'openai',
      account_type: 'oauth',
      status: 'active',
      schedulable: true,
      parent_account_id: null,
      missing: false,
      eligible: true,
      eligibility_reason: '',
      five_reset_at: t - 80,
      five_used_percent: 0,
      seven_reset_at: t + 500_000,
      seven_used_percent: 8,
      quota_fetched_at: t - 8,
      quota_state: 'ok',
      next_action_at: t + 12,
      runtime_state: 'due',
      last_error: '',
      last_answer_status: 'no_answer',
      last_answer_text: '',
      last_answer_at: t - 36_000,
      last_request_model: 'gpt-5.4',
      last_transport_path: 'official_codex_sse',
      last_answer_source: 'response.completed',
      last_quota_evidence: 'unconfirmed',
      last_terminal_summary: '请求成功 · 额度未确认',
      policy: { enabled: false, enable_generation: 1, model_override: null, grace_override_seconds: null, max_retries_override: null, retry_base_override_seconds: null },
    },
    {
      id: 'acc-blocked',
      remote_id: 31,
      remote_created_at: '2026-05-02T09:00:00Z',
      identity_generation: 1,
      name: 'weekly-cap',
      email: 'cap@example.com',
      plan_type: 'plus',
      platform: 'openai',
      account_type: 'oauth',
      status: 'active',
      schedulable: true,
      parent_account_id: null,
      missing: false,
      eligible: true,
      eligibility_reason: '',
      five_reset_at: t + 4_800,
      five_used_percent: 0,
      seven_reset_at: t + 86_400,
      seven_used_percent: 100,
      quota_fetched_at: t - 90,
      quota_state: 'ok',
      next_action_at: t + 86_430,
      runtime_state: 'blocked_7d',
      last_error: '',
      last_answer_status: 'normal',
      last_answer_text: '21',
      last_answer_at: t - 92_000,
      last_request_model: 'gpt-5.4',
      last_transport_path: 'official_codex_sse',
      last_answer_source: 'response.completed',
      last_quota_evidence: 'official_headers',
      last_terminal_summary: '7d 窗口已耗尽',
      policy: { enabled: true, enable_generation: 1, model_override: null, grace_override_seconds: null, max_retries_override: null, retry_base_override_seconds: null },
    },
    {
      id: 'acc-attention',
      remote_id: 44,
      remote_created_at: '2026-04-11T11:22:00Z',
      identity_generation: 2,
      name: 'oauth-stale',
      email: 'stale@example.com',
      plan_type: 'plus',
      platform: 'openai',
      account_type: 'oauth',
      status: 'error',
      schedulable: false,
      parent_account_id: null,
      missing: false,
      eligible: true,
      eligibility_reason: '',
      five_reset_at: t + 6_000,
      five_used_percent: 17,
      seven_reset_at: t + 300_000,
      seven_used_percent: 21,
      quota_fetched_at: t - 300,
      quota_state: 'error',
      next_action_at: t + 120,
      runtime_state: 'attention',
      last_error: 'quota 接口返回 401，需要重新授权该账号',
      last_answer_status: 'legacy_invalid',
      last_answer_text: '',
      last_answer_at: t - 240_000,
      last_request_model: '',
      last_transport_path: '',
      last_answer_source: '',
      last_quota_evidence: '',
      last_terminal_summary: '',
      policy: { enabled: false, enable_generation: 2, model_override: null, grace_override_seconds: null, max_retries_override: null, retry_base_override_seconds: null },
    },
    {
      id: 'acc-pro',
      remote_id: 9,
      remote_created_at: '2026-03-01T00:00:00Z',
      identity_generation: 1,
      name: 'pro-skip',
      email: 'pro@example.com',
      plan_type: 'pro',
      platform: 'openai',
      account_type: 'oauth',
      status: 'active',
      schedulable: true,
      parent_account_id: null,
      missing: false,
      eligible: false,
      eligibility_reason: 'Pro 套餐不在自动唤醒范围内',
      five_reset_at: undefined,
      five_used_percent: undefined,
      seven_reset_at: undefined,
      seven_used_percent: undefined,
      quota_fetched_at: t - 20,
      quota_state: 'skipped',
      next_action_at: undefined,
      runtime_state: 'disabled',
      last_error: '',
      last_answer_status: '',
      last_answer_text: '',
      last_request_model: '',
      last_transport_path: '',
      last_answer_source: '',
      last_quota_evidence: '',
      last_terminal_summary: '',
      policy: { enabled: false, enable_generation: 1, model_override: null, grace_override_seconds: null, max_retries_override: null, retry_base_override_seconds: null },
    },
  ]
}

function json(res: ServerResponse, status: number, data: unknown, error?: { code: string; message: string }) {
  res.statusCode = status
  res.setHeader('Content-Type', 'application/json')
  res.end(JSON.stringify(error ? { error } : { data }))
}

async function readBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = []
  for await (const chunk of req) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))
  if (!chunks.length) return {}
  try { return JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown> } catch { return {} }
}

export function previewMock(): Plugin {
  const accounts = sampleAccounts()
  const settings = {
    base_url: 'https://sub2api.example.com',
    api_key_configured: true,
    global_model: 'gpt-5.4',
    allow_private_http: false,
    sync_interval_seconds: 300,
    reset_grace_seconds: 30,
    max_retries: 3,
    retry_base_seconds: 30,
    request_timeout_seconds: 90,
    max_concurrency: 4,
    direct_wakeup_enabled: false,
    updated_at: '2026-08-29T00:00:00Z',
  }
  const events = [
    { id: 5, level: 'info', actor: 'scheduler', action: 'cycle_verified', account_id: 'acc-plus', message: 'plus@example.com 已验证新的 5h 窗口', metadata_json: '{}', created_at: now() - 420 },
    { id: 4, level: 'warning', actor: 'scheduler', action: 'answer_abnormal', account_id: 'acc-team', message: 'team@example.com 返回了非数字答案', metadata_json: '{}', created_at: now() - 1_800 },
    { id: 3, level: 'error', actor: 'scheduler', action: 'quota_unauthorized', account_id: 'acc-attention', message: 'stale@example.com quota 读取失败：401', metadata_json: '{}', created_at: now() - 3_600 },
    { id: 2, level: 'info', actor: 'admin', action: 'sync_accounts', account_id: '', message: '同步完成，发现 6 个 OpenAI OAuth 账号', metadata_json: '{}', created_at: now() - 7_200 },
    { id: 1, level: 'info', actor: 'admin', action: 'setup_complete', account_id: '', message: '初始设置已完成', metadata_json: '{}', created_at: now() - 86_400 },
  ]
  let authenticated = false
  let csrf = 'preview-csrf'

  const handle = async (req: IncomingMessage, res: ServerResponse, next: () => void) => {
    const url = req.url ?? ''
    const path = url.split('?')[0]
    if (!path.startsWith('/api/')) return next()

    const method = (req.method ?? 'GET').toUpperCase()
    if (path === '/api/setup/status' && method === 'GET') {
      return json(res, 200, { setup_complete: true, setup_available: false })
    }
    if (path === '/api/auth/login' && method === 'POST') {
      authenticated = true
      res.setHeader('Set-Cookie', [
        'sub2api_auto5h_session=preview-session; Path=/; HttpOnly; SameSite=Strict',
        `sub2api_auto5h_csrf=${csrf}; Path=/; SameSite=Strict`,
      ])
      return json(res, 200, { username: 'admin', csrf_token: csrf })
    }
    if (path === '/api/auth/logout' && method === 'POST') {
      authenticated = false
      return json(res, 200, { ok: true })
    }
    if (!authenticated) {
      return json(res, 401, null, { code: 'UNAUTHENTICATED', message: '请先登录' })
    }
    if (path === '/api/auth/session' && method === 'GET') return json(res, 200, { username: 'admin' })
    if (path === '/api/status' && method === 'GET') return json(res, 200, { global_pause: false, active_workers: 1 })
    if (path === '/api/accounts' && method === 'GET') return json(res, 200, accounts)
    if (path === '/api/accounts/sync' && method === 'POST') return json(res, 200, { accounts })
    if (path === '/api/accounts/batch-policy' && method === 'POST') {
      const body = await readBody(req)
      const ids = Array.isArray(body.ids) ? body.ids as string[] : []
      for (const id of ids) {
        const account = accounts.find((item) => item.id === id)
        if (account && account.eligible) {
          const policy = { ...(account.policy as Record<string, unknown>), enabled: Boolean(body.enabled) }
          account.policy = policy
        }
      }
      return json(res, 200, { failed: {} })
    }
    const accountMatch = path.match(/^\/api\/accounts\/([^/]+)(?:\/(.+))?$/)
    if (accountMatch) {
      const account = accounts.find((item) => item.id === accountMatch[1])
      if (!account) return json(res, 404, null, { code: 'NOT_FOUND', message: '账号不存在' })
      const rest = accountMatch[2] ?? ''
      if (rest === '' && method === 'GET') return json(res, 200, account)
      if (rest === 'policy' && method === 'PUT') {
        const body = await readBody(req)
        account.policy = { ...(account.policy as object), ...body, enable_generation: 1 }
        if (body.enabled) account.runtime_state = 'waiting'
        return json(res, 200, account)
      }
      if (rest === 'quota-refresh' && method === 'POST') return json(res, 200, account)
      if (rest === 'run' && method === 'POST') {
        account.runtime_state = 'verifying'
        return json(res, 200, { ok: true })
      }
      if (rest === 'models' && method === 'GET') return json(res, 200, ['gpt-5.4', 'gpt-5.3', 'gpt-text-e2e'])
      if (rest === 'cycles' && method === 'GET') {
        return json(res, 200, [
          { id: `cyc-${account.id}-1`, cycle_key: `${account.id}:5h:${now() - 18_000}`, kind: 'five_hour', source_reset_at: now() - 18_000, due_at: now() - 17_970, status: 'verified', attempt_count: 1, accepted_at: now() - 17_960, reason: '', created_at: now() - 18_000 },
          { id: `cyc-${account.id}-0`, cycle_key: `${account.id}:bootstrap`, kind: 'bootstrap', due_at: now() - 90_000, status: 'skipped_external', attempt_count: 0, reason: '外部流量已启动窗口', created_at: now() - 90_000 },
        ])
      }
    }
    const cycleMatch = path.match(/^\/api\/cycles\/([^/]+)\/attempts$/)
    if (cycleMatch && method === 'GET') {
      return json(res, 200, [
        {
          id: `att-${cycleMatch[1]}-1`,
          attempt_number: 1,
          started_at: now() - 17_980,
          ended_at: now() - 17_960,
          outcome: 'success',
          http_status: 200,
          error_code: '',
          message: 'response.completed',
          answer_status: 'normal',
          answer_text: '21',
          request_model: 'gpt-5.4',
          transport_path: 'official_codex_sse',
          answer_source: 'response.completed',
          quota_evidence: 'official_headers',
          terminal_summary: '已验证',
        },
      ])
    }
    if (path === '/api/events' && method === 'GET') return json(res, 200, events)
    if (path === '/api/settings' && method === 'GET') return json(res, 200, settings)
    if (path === '/api/settings' && method === 'PUT') {
      const body = await readBody(req)
      Object.assign(settings, body, { api_key: undefined, api_key_configured: true })
      return json(res, 200, settings)
    }
    if (path === '/api/settings/test' && method === 'POST') return json(res, 200, { version: '0.1.183' })
    if (path === '/api/auth/password' && method === 'PUT') return json(res, 200, { ok: true })
    return json(res, 404, null, { code: 'NOT_FOUND', message: 'API endpoint not found' })
  }

  return {
    name: 'preview-mock-api',
    configureServer(server) {
      server.middlewares.use((req, res, next) => { void handle(req, res, next) })
    },
    configurePreviewServer(server) {
      server.middlewares.use((req, res, next) => { void handle(req, res, next) })
    },
  }
}
