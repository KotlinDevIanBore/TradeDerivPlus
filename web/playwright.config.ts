import { defineConfig, devices } from '@playwright/test'

/**
 * End-to-end tests run against the real stack in mock mode: a real API, a real
 * database, a simulated market feed and a simulated M-Pesa lifecycle. Nothing
 * here stubs the network, because the point is to catch the things unit tests
 * cannot.
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false, // these share one database
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? [['html'], ['list']] : 'list',
  timeout: 60_000,
  expect: { timeout: 15_000 },

  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },

  projects: [
    { name: 'mobile', use: { ...devices['Pixel 7'] }, testMatch: /user\..*\.spec\.ts/ },
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
  ],

  // Started only when nothing is already listening, so a local run reuses the
  // dev server rather than fighting it for the port.
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173',
    reuseExistingServer: true,
    timeout: 120_000,
  },
})
