import { expect, test } from '@playwright/test'
import { readFile } from 'node:fs/promises'

async function setupToken(): Promise<string> {
  for (let attempt = 0; attempt < 50; attempt++) {
    const log = await readFile('../.e2e-data/app.log', 'utf8').catch(() => '')
    for (const line of log.split('\n')) {
      try {
        const event = JSON.parse(line) as { msg?: string; token?: string }
        if (event.msg === 'first-run setup token' && event.token) return event.token
      } catch { /* log line may be incomplete while the server starts */ }
    }
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  throw new Error('setup token was not written to the application log')
}

async function initialize(page: import('@playwright/test').Page) {
  await page.goto('/')
  if (await page.getByRole('heading', { name: '初始化' }).isVisible().catch(() => false)) {
    await page.getByLabel('Setup Token').fill(await setupToken())
    await page.getByLabel('管理员密码').fill('correct horse battery staple')
    await page.getByLabel('sub2api 地址').fill('http://127.0.0.1:18081')
    await page.getByLabel('Admin API Key').fill('e2e-admin-key')
    await page.getByLabel('默认文本模型').fill('gpt-text-e2e')
    await page.getByLabel('允许回环或私网 IP 使用 HTTP').check()
    await page.getByRole('button', { name: '完成初始化' }).click()
  }
  await expect(page.getByRole('button', { name: '登录' })).toBeVisible()
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill('correct horse battery staple')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('heading', { name: '账号' })).toBeVisible()
}

test('initializes, logs in, syncs and enables an account', async ({ page }) => {
  await initialize(page)
  await page.getByTitle('同步账号').click()
  await expect(page.getByText('plus@example.com').first()).toBeVisible()
  await page.getByRole('button', { name: /plus@example.com/ }).click()
  const toggle = page.getByLabel('自动激活')
  await toggle.check()
  await page.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('等待刷新').last()).toBeVisible()

  const body = await page.locator('body').boundingBox()
  expect(body?.width).toBeLessThanOrEqual(page.viewportSize()!.width)
})

test('deep links render SPA and API 404 stays JSON', async ({ page, request }) => {
  await page.goto('/settings')
  await expect(page.locator('#app')).toBeVisible()
  const response = await request.get('/api/not-real')
  expect(response.status()).toBe(404)
  expect(response.headers()['content-type']).toContain('application/json')
  expect(await response.json()).toMatchObject({ error: { code: 'NOT_FOUND' } })
})
