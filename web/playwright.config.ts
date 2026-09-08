import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';

const evidence = process.env.CLUSTERFORGE_TEST_EVIDENCE_DIR;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  outputDir: evidence ? path.join(evidence, 'test-results') : 'test-results',
  reporter: evidence ? [['list'], ['json', { outputFile: path.join(evidence, 'playwright.json') }]] : 'list',
  use: {
    baseURL: 'http://127.0.0.1:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'pnpm dev --host 127.0.0.1',
    url: 'http://127.0.0.1:5173',
    reuseExistingServer: !process.env.CI,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1920, height: 1080 } } }],
});
