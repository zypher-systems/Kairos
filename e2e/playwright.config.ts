import { defineConfig } from '@playwright/test'

const baseURL = process.env.KAIROS_BASE_URL || 'http://127.0.0.1:8080'

export default defineConfig({
  testDir: './tests',
  timeout: 90_000,
  expect: { timeout: 20_000 },
  retries: 0,
  fullyParallel: false,
  workers: 1,
  globalSetup: './global-setup.ts',
  use: {
    baseURL,
    viewport: { width: 1920, height: 1080 },
    // One saved session for every test (see global-setup.ts).
    storageState: '.auth/state.json',
    // The server's CSRF guard rejects mutating API calls with no Origin; a
    // browser always sends one, Playwright's request fixture does not.
    extraHTTPHeaders: { Origin: baseURL },
  },
})
