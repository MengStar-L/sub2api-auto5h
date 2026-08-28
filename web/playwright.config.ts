import { defineConfig, devices } from '@playwright/test'

const appCommand = [
  'rm -rf ../.e2e-data && mkdir -p ../.e2e-data && env',
  'SUB2API_AUTO5H_LISTEN=127.0.0.1:18080',
  'SUB2API_AUTO5H_DB_PATH=../.e2e-data/app.db',
  'SUB2API_AUTO5H_MASTER_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
  '../build/e2e/sub2api-auto5h serve 2>&1 | tee ../.e2e-data/app.log',
].join(' ')

export default defineConfig({
  testDir: './tests',
  testMatch: /.*\.spec\.ts/,
  timeout: 45_000,
  expect: { timeout: 8_000 },
  reporter: [['list']],
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:18080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: [
    { command: 'node tests/fake-sub2api.mjs', url: 'http://127.0.0.1:18081/health', reuseExistingServer: false },
    { command: appCommand, url: 'http://127.0.0.1:18080/api/setup/status', reuseExistingServer: false, timeout: 30_000 },
  ],
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'mobile', use: { ...devices['Pixel 7'], viewport: { width: 390, height: 844 } } },
  ],
})
