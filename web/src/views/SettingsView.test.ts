import { flushPromises, mount } from '@vue/test-utils'
import { api } from '../api'
import SettingsView from './SettingsView.vue'

vi.mock('../api', () => ({ api: vi.fn() }))

const settings = {
  base_url: 'https://sub2api.example.com',
  api_key_configured: true,
  global_model: 'gpt-text',
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

describe('SettingsView', () => {
  it('requires risk consent and submits only writable settings fields', async () => {
    const apiMock = vi.mocked(api)
    apiMock.mockResolvedValueOnce(settings).mockResolvedValueOnce({})
    const wrapper = mount(SettingsView)
    await flushPromises()

    await wrapper.get('input[aria-label="启用官方 Codex 直连唤醒"]').setValue(true)
    const save = wrapper.get<HTMLButtonElement>('button[type="submit"]')
    expect(save.element.disabled).toBe(true)

    await wrapper.get('.risk-ack input').setValue(true)
    expect(save.element.disabled).toBe(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(apiMock).toHaveBeenCalledTimes(2)
    const [path, init] = apiMock.mock.calls[1]
    expect(path).toBe('/api/settings')
    const payload = JSON.parse(String(init?.body)) as Record<string, unknown>
    expect(payload).toMatchObject({ direct_wakeup_enabled: true, direct_wakeup_risk_acknowledged: true })
    expect(payload).not.toHaveProperty('api_key_configured')
    expect(payload).not.toHaveProperty('updated_at')
  })
})
