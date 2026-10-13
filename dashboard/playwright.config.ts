import { defineConfig, devices } from '@playwright/test'

const port = 4173
const isCI = !!process.env.CI

export default defineConfig({
    testDir: './e2e',
    fullyParallel: true,
    forbidOnly: isCI,
    reporter: isCI ? [['github'], ['html', { open: 'never' }]] : 'list',
    use: {
        baseURL: `http://localhost:${port}`,
        trace: 'retain-on-failure',
    },
    projects: [
        {
            name: 'chromium',
            use: { ...devices['Desktop Chrome'] },
        },
    ],
    // Tests run against the production build, which is what ddup serves
    // The API is mocked in each test
    // This calls the binaries directly, because pnpm starts scripts in a new process group, which Playwright can't stop after the tests
    webServer: {
        command: `tsc && vite build && vite preview --port ${port} --strictPort`,
        url: `http://localhost:${port}`,
        reuseExistingServer: !isCI,
    },
})
