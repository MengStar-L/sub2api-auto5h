export interface Policy {
  enabled: boolean
  enable_generation: number
  model_override?: string | null
  grace_override_seconds?: number | null
  max_retries_override?: number | null
  retry_base_override_seconds?: number | null
}

export interface Account {
  id: string
  remote_id: number
  remote_created_at: string
  identity_generation: number
  name: string
  email: string
  plan_type: string
  platform: string
  account_type: string
  status: string
  schedulable: boolean
  parent_account_id?: number | null
  missing: boolean
  eligible: boolean
  eligibility_reason: string
  five_reset_at?: number
  five_used_percent?: number
  seven_reset_at?: number
  seven_used_percent?: number
  quota_fetched_at?: number
  quota_state: string
  next_action_at?: number
  runtime_state: string
  last_error?: string
  last_answer_status: string
  last_answer_text: string
  last_answer_at?: number
  policy: Policy
}

export interface Cycle {
  id: string
  cycle_key: string
  kind: string
  source_reset_at?: number
  due_at: number
  status: string
  attempt_count: number
  accepted_at?: number
  reason: string
  created_at: number
}

export interface Attempt {
  id: string
  attempt_number: number
  started_at: number
  ended_at?: number
  outcome: string
  http_status?: number
  error_code: string
  message: string
  answer_status: string
  answer_text: string
}

export interface EventItem {
  id: number
  level: string
  actor: string
  action: string
  account_id?: string
  message: string
  metadata_json: string
  created_at: number
}

export interface Settings {
  base_url: string
  api_key?: string
  api_key_configured?: boolean
  global_model: string
  allow_private_http: boolean
  sync_interval_seconds: number
  reset_grace_seconds: number
  max_retries: number
  retry_base_seconds: number
  request_timeout_seconds: number
  max_concurrency: number
  updated_at?: string
}
