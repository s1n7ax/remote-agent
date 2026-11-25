import { defineConfig, devices } from '@playwright/test';

// The browsers come from nixpkgs (PLAYWRIGHT_BROWSERS_PATH in the dev shell),
// so @playwright/test is pinned to nixpkgs' playwright-driver version.
export default defineConfig({
  testDir: './tests',
  timeout: 120_000,
  expect: { timeout: 15_000 },
  retries: 0,
  workers: 1,
  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',
  use: {
    baseURL: 'http://127.0.0.1:17700',
    ...devices['Pixel 7'],
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    actionTimeout: 15_000,
  },
});
