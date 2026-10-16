import { expect, test, type Page } from '@playwright/test'

// Same shape as the build info the server writes into the page
interface BuildInfo {
    build: string
    version: string
    buildId?: string
    commit?: string
    buildDate?: string
}

const pageBuild: BuildInfo = {
    build: '1.2.3|build-42|0123456789abcdef',
    version: '1.2.3',
    buildId: 'build-42',
    commit: '0123456789abcdef',
    buildDate: '2026-01-02T03:04:05Z',
}
const newerBuild = '1.2.4|build-43|fedcba9876543210'

const buildHeader = 'X-Ddup-Build'

// Serves the page like the server does, with the build info in a JSON script element before </head>
// The preview server of Vite doesn't do it
// The info is a function of the number of the request when the page changes between reloads, and it can be raw text to send invalid JSON
// Returns an object that counts how many times the page was requested
const servePage = async (page: Page, info: BuildInfo | string | null | ((request: number) => BuildInfo)) => {
    const served = { documents: 0 }
    await page.route(
        (url) => url.pathname === '/',
        async (route) => {
            served.documents++
            if (!info) {
                await route.continue()
                return
            }

            const current = typeof info === 'function' ? info(served.documents) : info
            const json = typeof current === 'string' ? current : JSON.stringify(current)
            const response = await route.fetch()
            const body = (await response.text()).replace(
                '</head>',
                `<script type="application/json" id="ddup-build">${json}</script></head>`
            )
            await route.fulfill({ response, body })
        }
    )
    return served
}

// Mocks the /api/status endpoint with an empty list of domains, and the build in the header
// Change build to change the next responses; a null build sends no header
const mockStatus = async (page: Page, build: string | null) => {
    const mock = { build, requests: 0 }
    await page.route('**/api/status', (route) => {
        mock.requests++
        return route.fulfill({ json: {}, headers: mock.build ? { [buildHeader]: mock.build } : {} })
    })
    return mock
}

const footer = (page: Page) => page.locator('footer')

test.describe('footer', () => {
    test.beforeEach(async ({ page }) => {
        await mockStatus(page, pageBuild.build)
    })

    test('shows the version and the short commit from the page', async ({ page }) => {
        await servePage(page, pageBuild)
        await page.goto('/')

        await expect(footer(page)).toHaveText('ddup 1.2.3 · 0123456')
    })

    test('leaves out the commit when the build does not set it', async ({ page }) => {
        await servePage(page, { build: 'canary||', version: 'canary' })
        await page.goto('/')

        await expect(footer(page)).toHaveText('ddup canary')
    })

    test('is not shown when the page has no build info', async ({ page }) => {
        await servePage(page, null)
        await page.goto('/')

        await expect(page.getByText('No domains configured or available')).toBeVisible()
        await expect(footer(page)).toHaveCount(0)
    })

    test('is not shown when the build info is invalid', async ({ page }) => {
        await servePage(page, '{"build":1}')
        await page.goto('/')

        await expect(page.getByText('No domains configured or available')).toBeVisible()
        await expect(footer(page)).toHaveCount(0)
    })
})

test.describe('footer position', () => {
    test('does not move while the status is refreshed', async ({ page }) => {
        await servePage(page, pageBuild)

        // The response of a refresh can be held, to look at the page while the request is in flight
        let hold = false
        let release: (() => void) | undefined
        await page.route('**/api/status', async (route) => {
            if (hold) {
                await new Promise<void>((resolve) => {
                    release = resolve
                })
            }
            await route.fulfill({
                json: {
                    'a.example': {
                        lastUpdated: '2026-10-13T09:30:00Z',
                        provider: 'cloudflare',
                        endpoints: [{ healthy: true, ip: '192.0.2.1' }],
                    },
                },
                headers: { [buildHeader]: pageBuild.build },
            })
        })
        await page.goto('/')
        await expect(page.getByText('a.example', { exact: true })).toBeVisible()
        await expect(footer(page)).toBeVisible()
        const before = (await footer(page).boundingBox())?.y

        hold = true
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()
        await expect.poll(() => release !== undefined).toBe(true)

        // The list stays, there is no loading block, and the footer is where it was
        await expect(page.getByText('a.example', { exact: true })).toBeVisible()
        await expect(page.getByText('Loading domain data...')).toHaveCount(0)
        expect((await footer(page).boundingBox())?.y).toBe(before)

        hold = false
        release?.()
        await expect(page.getByRole('button', { name: 'Check now' })).toBeEnabled()
        expect((await footer(page).boundingBox())?.y).toBe(before)
    })
})

test.describe('reload when the server is upgraded', () => {
    test('reloads once when the build in the header is different', async ({ page }) => {
        const served = await servePage(page, pageBuild)
        const status = await mockStatus(page, newerBuild)
        await page.goto('/')

        // The page is requested again, and then the reload is not repeated right away
        await expect.poll(() => served.documents).toBe(2)
        await expect.poll(() => status.requests).toBe(2)
        await expect(page.getByText('No domains configured or available')).toBeVisible()
        expect(served.documents).toBe(2)
    })

    test('shows the new version after the reload', async ({ page }) => {
        // The first request gets the page of the old build, and the reload gets the page of the new one
        const served = await servePage(page, (request) =>
            request === 1 ? pageBuild : { build: newerBuild, version: '1.2.4', commit: 'fedcba9876543210' }
        )
        await mockStatus(page, newerBuild)
        await page.goto('/')

        await expect(footer(page)).toHaveText('ddup 1.2.4 · fedcba9')
        expect(served.documents).toBe(2)
    })

    test('does not reload when the build in the header is the same', async ({ page }) => {
        const served = await servePage(page, pageBuild)
        const status = await mockStatus(page, pageBuild.build)
        await page.goto('/')
        await expect(page.getByText('No domains configured or available')).toBeVisible()

        // Refresh now checks the build too
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()
        await expect.poll(() => status.requests).toBe(2)

        expect(served.documents).toBe(1)
    })

    test('does not reload when there is no header', async ({ page }) => {
        const served = await servePage(page, pageBuild)
        const status = await mockStatus(page, null)
        await page.goto('/')
        await expect(page.getByText('No domains configured or available')).toBeVisible()
        expect(status.requests).toBe(1)

        expect(served.documents).toBe(1)
    })

    test('does not reload when the page has no build info', async ({ page }) => {
        const served = await servePage(page, null)
        const status = await mockStatus(page, newerBuild)
        await page.goto('/')
        await expect(page.getByText('No domains configured or available')).toBeVisible()

        expect(status.requests).toBe(1)
        expect(served.documents).toBe(1)
    })

    test('stops after 3 reloads when the server keeps sending a different build', async ({ page }) => {
        await page.clock.install()
        const served = await servePage(page, pageBuild)
        const status = await mockStatus(page, newerBuild)
        await page.goto('/')
        await expect.poll(() => served.documents).toBe(2)
        await expect.poll(() => status.requests).toBe(2)

        // Each auto-refresh is at least 30 seconds after the last reload, so it can reload again until the limit
        for (const documents of [3, 4, 4]) {
            const requests = status.requests
            await page.clock.fastForward('01:00')
            await expect.poll(() => status.requests).toBeGreaterThan(requests)
            await expect.poll(() => served.documents).toBe(documents)
        }

        // The page is still working
        await expect(page.getByText('No domains configured or available')).toBeVisible()
    })

    test('waits 30 seconds between reloads', async ({ page }) => {
        await page.clock.install()
        const served = await servePage(page, pageBuild)
        const status = await mockStatus(page, newerBuild)
        await page.goto('/')
        await expect.poll(() => served.documents).toBe(2)
        await expect.poll(() => status.requests).toBe(2)

        // Refresh now right after the reload doesn't reload again
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()
        await expect.poll(() => status.requests).toBe(3)
        expect(served.documents).toBe(2)

        // After 30 seconds it does
        await page.clock.fastForward('00:30')
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()
        await expect.poll(() => served.documents).toBe(3)
    })

    test('reloads again for a newer build after the limit was reached for another', async ({ page }) => {
        await page.clock.install()
        const served = await servePage(page, pageBuild)
        const status = await mockStatus(page, newerBuild)
        await page.goto('/')
        await expect.poll(() => served.documents).toBe(2)

        // A different server build starts a new count
        status.build = '1.2.5|build-44|0000000000000000'
        await page.clock.fastForward('01:00')
        await expect.poll(() => served.documents).toBe(3)
    })

    test('never reloads when the session storage is not available', async ({ page }) => {
        await page.addInitScript(() => {
            Object.defineProperty(window, 'sessionStorage', {
                get() {
                    throw new Error('denied')
                },
            })
        })
        const served = await servePage(page, pageBuild)
        const status = await mockStatus(page, newerBuild)
        await page.goto('/')
        await expect(footer(page)).toHaveText('ddup 1.2.3 · 0123456')

        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()
        await expect.poll(() => status.requests).toBe(2)

        expect(served.documents).toBe(1)
    })
})
