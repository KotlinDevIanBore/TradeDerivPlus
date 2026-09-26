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
    // 5174, not the 5173 a developer runs the app on. See the webServer note.
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5174',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },

  /**
   * Two sets of projects, because the app has two trading surfaces and the flag
   * that chooses between them is server state, not a per-test option. The digit
   * projects need an API started with DIGIT_CONTRACTS_ENABLED=true, and the
   * others need it off, so CI gives each its own job with its own database — the
   * flag row in app_settings is seeded once and never overwritten, so one
   * process cannot serve both modes.
   *
   * **Always name the projects.** A bare `npx playwright test` runs all four,
   * which means running half of them against an API configured for the other
   * half: every one of those waits out its timeout for a surface that will never
   * render. Playwright has no way to keep a project out of an unfiltered run, so
   * the filter has to come from the command — and the specs now refuse to start
   * when their surface is off, which is what makes the mistake legible instead
   * of merely slow. See requireFeatures in e2e/helpers.ts.
   *
   *   npx playwright test --project=mobile --project=mobile-narrow --project=desktop        → CFD
   *   npx playwright test --project=digit-mobile --project=digit-narrow --project=digit-desktop
   *
   * Two phone widths, not one. Pixel 7 is 412px wide, and every mobile layout
   * defect in docs/investigation-reports/mobile-trading-screen-layout.md was
   * invisible at exactly that width: the contract-type tab row overflows by 28px
   * at 375 and fits by 9px at 412; the digit picker wraps to two rows at 375 and
   * fits at 412; the chart keeps 237px at 412x915 and 0px at 375x667. A suite
   * that only ever looked at 412 could not have failed on any of them.
   *
   * The narrow projects use the iPhone 13 Mini descriptor (375 wide, the width
   * of every iPhone from the SE to the 13 mini) at the iPhone SE's 667px height,
   * so one project covers both the narrowest common width and the shortest
   * common height. Chromium rather than the descriptor's WebKit, because CI
   * installs chromium only -- the point is the viewport, not the engine.
   */
  projects: [
    { name: 'mobile', use: { ...devices['Pixel 7'] }, testMatch: /user\..*\.spec\.ts/ },
    {
      name: 'mobile-narrow',
      use: { ...devices['iPhone 13 Mini'], browserName: 'chromium', viewport: { width: 375, height: 667 } },
      testMatch: /user\..*\.spec\.ts/,
    },
    {
      name: 'desktop',
      use: { ...devices['Desktop Chrome'] },
      testIgnore: /digit\..*\.spec\.ts/,
    },
    {
      name: 'digit-mobile',
      use: { ...devices['Pixel 7'] },
      testMatch: /digit\..*\.spec\.ts/,
    },
    {
      name: 'digit-narrow',
      use: { ...devices['iPhone 13 Mini'], browserName: 'chromium', viewport: { width: 375, height: 667 } },
      testMatch: /digit\..*\.spec\.ts/,
    },
    {
      name: 'digit-desktop',
      use: { ...devices['Desktop Chrome'] },
      testMatch: /digit\..*\.spec\.ts/,
    },
  ],

  /**
   * The suite runs its own dev server on its own port, and that is the whole
   * point of both halves.
   *
   * A dev server, because it serves the working tree. `vite preview` serves a
   * built `dist/`, so a run against one verifies whatever was last built — which
   * silently happened here: a preview server on 5173 was reused for a whole
   * phase, and a test reported a component missing that was present in the source
   * and absent only from the stale bundle.
   *
   * Its own port, because `reuseExistingServer` cannot tell what it is reusing.
   * On 5173 it adopts whatever a developer happens to be running; on 5174 it
   * adopts only a previous test server, which serves the same source and is safe
   * to keep. So the reuse stays (runs are fast) without the trust problem.
   *
   * Point somewhere else with E2E_BASE_URL when testing a deployed build; the
   * server below is skipped when that URL is already answering.
   */
  webServer: {
    command: 'npm run dev -- --port 5174 --strictPort',
    url: 'http://localhost:5174',
    reuseExistingServer: true,
    timeout: 120_000,
  },
})
