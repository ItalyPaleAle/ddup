import { expect, test, type Locator, type Page, type Request } from '@playwright/test'

// Same shape as the response of the /api/status endpoint
interface DomainStatusEndpoint {
    healthy: boolean
    ip: string
    failureCount?: number
    since?: string
    sinceStart?: boolean
    lastFailure?: string
    lastError?: string
    firstFailure?: string
}

interface DomainStatus {
    lastUpdated: string
    provider: string
    error?: string
    endpoints: DomainStatusEndpoint[]
}

type DomainsResponse = Record<string, DomainStatus>

const lastUpdated = '2026-01-02T03:04:05Z'

// One domain for each state the dashboard can show
// Healthy: healthy.example, ipv6.example
// Warning: partial.example
// Unhealthy: down.example, dns-error.example, no-endpoints.example
const domains: DomainsResponse = {
    'healthy.example': {
        lastUpdated,
        provider: 'cloudflare',
        endpoints: [
            { healthy: true, ip: '192.0.2.1' },
            { healthy: true, ip: '192.0.2.2', failureCount: 1 },
        ],
    },
    'partial.example': {
        lastUpdated,
        provider: 'ovh',
        endpoints: [
            { healthy: true, ip: '198.51.100.1' },
            { healthy: false, ip: '198.51.100.2', failureCount: 3 },
        ],
    },
    'down.example': {
        lastUpdated,
        provider: 'azure',
        endpoints: [{ healthy: false, ip: '203.0.113.1', failureCount: 5 }],
    },
    'dns-error.example': {
        lastUpdated,
        provider: 'cloudflare',
        error: 'Error updating DNS records: boom',
        endpoints: [{ healthy: true, ip: '203.0.113.9' }],
    },
    'no-endpoints.example': {
        lastUpdated,
        provider: 'cloudflare',
        endpoints: [],
    },
    'ipv6.example': {
        lastUpdated,
        provider: 'ovh',
        endpoints: [{ healthy: true, ip: '2001:db8::1' }],
    },
}
const domainNames = Object.keys(domains)

// Same as domains, but down.example has recovered
const recoveredDomains: DomainsResponse = {
    ...domains,
    'down.example': {
        lastUpdated,
        provider: 'azure',
        endpoints: [{ healthy: true, ip: '203.0.113.1' }],
    },
}

// Domains for the endpoint history, with an endpoint in each situation
const historyDomains: DomainsResponse = {
    'history.example': {
        lastUpdated: '2026-10-13T09:30:00Z',
        provider: 'cloudflare',
        endpoints: [
            // Recovered: it failed before, and the last failure is kept
            { healthy: true, ip: '192.0.2.10', since: '2026-10-09T08:15:00Z', lastFailure: '2026-10-09T08:10:00Z' },
            // Failing
            {
                healthy: false,
                ip: '192.0.2.11',
                failureCount: 3,
                since: '2026-10-13T09:00:00Z',
                lastFailure: '2026-10-13T09:29:00Z',
                lastError: 'connection refused',
                firstFailure: '2026-10-13T09:00:00Z',
            },
            // Healthy since the first check after ddup started
            { healthy: true, ip: '192.0.2.12', since: '2026-10-13T07:45:00Z', sinceStart: true },
            // From a server that does not report the times
            { healthy: true, ip: '192.0.2.13' },
            // Warning: healthy, but the last checks failed
            {
                healthy: true,
                ip: '192.0.2.14',
                failureCount: 2,
                since: '2026-10-12T10:00:00Z',
                firstFailure: '2026-10-13T09:27:00Z',
                lastFailure: '2026-10-13T09:28:00Z',
                lastError: 'status 503',
            },
            // Warning after a single failed check
            {
                healthy: true,
                ip: '192.0.2.15',
                failureCount: 1,
                since: '2026-10-12T10:00:00Z',
                firstFailure: '2026-10-13T09:28:30Z',
                lastFailure: '2026-10-13T09:28:30Z',
                lastError: 'status 500',
            },
        ],
    },
    'other.example': {
        lastUpdated: '2026-10-13T09:31:00Z',
        provider: 'ovh',
        endpoints: [{ healthy: true, ip: '198.51.100.7', since: '2026-10-12T22:00:00Z' }],
    },
}

// Returns a response with only some of the domains
const pickDomains = (from: DomainsResponse, ...names: string[]): DomainsResponse =>
    Object.fromEntries(names.map((name) => [name, from[name]]))

// The status shown by the icon in the browser tab and in the header
type OverallStatus = 'healthy' | 'warning' | 'unhealthy' | 'unknown'

// Mocks the /api/status endpoint
// Change body to change the next responses
const mockStatus = async (page: Page, body: DomainsResponse) => {
    const mock = { body, requests: 0 }
    await page.route('**/api/status', (route) => {
        mock.requests++
        return route.fulfill({ json: mock.body })
    })
    return mock
}

// Returns a promise that the test resolves by calling release, to control when a mocked response is sent
const deferred = () => {
    let release!: () => void
    const promise = new Promise<void>((resolve) => {
        release = resolve
    })
    return { promise, release }
}

const cardTitles = (page: Page) => page.locator('[data-slot="card-title"]')

const domainCard = (page: Page, name: string) =>
    page.locator('[data-slot="card"]').filter({ has: page.getByText(name, { exact: true }) })

const domainBadge = (page: Page, name: string) =>
    domainCard(page, name).locator('[data-slot="card-header"] [data-slot="badge"]')

// The row of an endpoint is the closest element around the IP that includes the failure count
const endpointRow = (card: Locator, ip: string) =>
    card.getByText(ip, { exact: true }).locator('xpath=ancestor::div[contains(., "Failures:")][1]')

// The count is the first span of a segment in the summary bar
const summaryCount = (
    page: Page,
    title: 'Healthy domains' | 'Domains with some unhealthy endpoints' | 'Unhealthy domains'
) => page.getByTitle(title, { exact: true }).locator('span').first()

const expectSummary = async (page: Page, healthy: number, warning: number, unhealthy: number) => {
    await expect(summaryCount(page, 'Healthy domains')).toHaveText(String(healthy))
    await expect(summaryCount(page, 'Domains with some unhealthy endpoints')).toHaveText(String(warning))
    await expect(summaryCount(page, 'Unhealthy domains')).toHaveText(String(unhealthy))
}

const historyButton = (page: Page, domain: string) => page.getByRole('button', { name: `History of ${domain}` })

const historyDialog = (page: Page, domain: string) =>
    page.getByRole('dialog', { name: `Endpoint history of ${domain}` })

const tabIcon = (page: Page) => page.locator('head link[rel~="icon"]')

const expectStatusIcon = async (page: Page, status: OverallStatus) => {
    await expect(page.getByRole('img', { name: `Overall status: ${status}`, exact: true })).toBeVisible()

    // The static icon from index.html is replaced, so the browser can't keep using it
    // The new one is the pre-rendered PNG of the status, bundled as a separate file instead of a data URL
    await expect(tabIcon(page)).toHaveCount(1)
    await expect(tabIcon(page)).toHaveAttribute('type', 'image/png')
    await expect(tabIcon(page)).toHaveAttribute('href', new RegExp(`^/assets/${status}-[\\w-]+\\.png$`))

    const href = await tabIcon(page).evaluate((link: HTMLLinkElement) => link.href)
    const response = await page.request.get(href)
    expect(response.ok()).toBe(true)
    expect(response.headers()['content-type']).toBe('image/png')
}

test.describe('domain list', () => {
    test.beforeEach(async ({ page }) => {
        await mockStatus(page, domains)
        await page.goto('/')
        await expect(cardTitles(page)).toHaveText(domainNames)
    })

    test('shows the health of each domain', async ({ page }) => {
        await expect(domainBadge(page, 'healthy.example')).toHaveAccessibleName('Healthy')
        await expect(domainBadge(page, 'ipv6.example')).toHaveAccessibleName('Healthy')
        await expect(domainBadge(page, 'partial.example')).toHaveAccessibleName('Warning')
        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Unhealthy')
        // A DNS error makes the domain unhealthy, even if its endpoints are healthy
        await expect(domainBadge(page, 'dns-error.example')).toHaveAccessibleName('Unhealthy')
        await expect(domainBadge(page, 'no-endpoints.example')).toHaveAccessibleName('Unhealthy')

        await expect(page.getByText('Last updated:')).toBeVisible()
    })

    test('the health of a domain is an icon before its name, named on hover', async ({ page }) => {
        const badge = domainBadge(page, 'partial.example')
        await expect(badge).toHaveText('')
        await expect(badge.locator('svg')).toHaveCount(1)
        await expect(badge).toHaveAttribute('role', 'img')
        await expect(badge).toHaveAttribute('title', 'Warning')

        // On the same line as the name, right before it
        const title = domainCard(page, 'partial.example').locator('[data-slot="card-title"]')
        const badgeBox = (await badge.boundingBox())!
        const titleBox = (await title.boundingBox())!
        expect(badgeBox.x + badgeBox.width).toBeLessThanOrEqual(titleBox.x)
        expect(titleBox.x - (badgeBox.x + badgeBox.width)).toBeLessThan(16)
        expect(Math.abs(badgeBox.y + badgeBox.height / 2 - (titleBox.y + titleBox.height / 2))).toBeLessThan(4)
    })

    test('shows the details of each domain', async ({ page }) => {
        const card = domainCard(page, 'partial.example')
        // The provider is configuration, not status, so the card does not show it
        await expect(card).not.toContainText('Provider')
        await expect(card).toContainText('1/2 endpoints healthy')
        await expect(card).toContainText('Endpoints (2)')

        // The health of an endpoint is an icon, named on hover
        const healthyRow = endpointRow(card, '198.51.100.1')
        await expect(healthyRow.getByRole('img', { name: 'Healthy', exact: true })).toHaveAttribute('title', 'Healthy')
        await expect(healthyRow).toContainText('Failures: 0')
        const unhealthyRow = endpointRow(card, '198.51.100.2')
        await expect(unhealthyRow.getByRole('img', { name: 'Unhealthy', exact: true })).toHaveAttribute(
            'title',
            'Unhealthy'
        )
        await expect(unhealthyRow).toContainText('Failures: 3')
        for (const row of [healthyRow, unhealthyRow]) {
            await expect(row.locator('[data-slot="badge"]')).toHaveText('')
        }

        // Endpoints that are still healthy can have recent failures
        await expect(endpointRow(domainCard(page, 'healthy.example'), '192.0.2.2')).toContainText('Failures: 1')

        await expect(domainCard(page, 'dns-error.example')).toContainText('Domain Error')
        await expect(domainCard(page, 'dns-error.example')).toContainText('Error updating DNS records: boom')

        const emptyCard = domainCard(page, 'no-endpoints.example')
        await expect(emptyCard).toContainText('0/0 endpoints healthy')
        await expect(emptyCard).not.toContainText('Endpoints (')
    })

    for (const colorScheme of ['light', 'dark'] as const) {
        test(`the endpoint badges have the colors of the domain badges, also on hover (${colorScheme} mode)`, async ({
            page,
        }) => {
            await page.emulateMedia({ colorScheme })
            // The badges have a transition on the text color, so read the colors once it's over
            const colors = async (badge: Locator) => {
                await page.evaluate(() =>
                    Promise.all(document.getAnimations().map((animation) => animation.finished.catch(() => {})))
                )
                return badge.evaluate((el) => {
                    const style = getComputedStyle(el)
                    return { background: style.backgroundColor, text: style.color }
                })
            }

            const card = domainCard(page, 'partial.example')
            for (const [ip, domain] of [
                ['198.51.100.1', 'healthy.example'],
                ['198.51.100.2', 'down.example'],
            ]) {
                const badge = endpointRow(card, ip).locator('[data-slot="badge"]')
                await page.mouse.move(0, 0)
                const atRest = await colors(badge)
                expect(atRest).toEqual(await colors(domainBadge(page, domain)))

                await badge.hover()
                expect(await colors(badge)).toEqual(atRest)
            }
        })
    }

    test('counts the domains by health', async ({ page }) => {
        await expectSummary(page, 2, 1, 3)
    })

    test('search filters by domain name', async ({ page }) => {
        const search = page.getByPlaceholder('Search domains or IP addresses...')

        await search.fill('PARTIAL')
        await expect(cardTitles(page)).toHaveText(['partial.example'])
        await expectSummary(page, 0, 1, 0)

        await search.fill('')
        await expect(cardTitles(page)).toHaveText(domainNames)
        await expectSummary(page, 2, 1, 3)
    })

    test('search filters by endpoint IP', async ({ page }) => {
        const search = page.getByPlaceholder('Search domains or IP addresses...')

        await search.fill('2001:DB8')
        await expect(cardTitles(page)).toHaveText(['ipv6.example'])

        // Unhealthy endpoints match too
        await search.fill('198.51.100.2')
        await expect(cardTitles(page)).toHaveText(['partial.example'])
    })

    test('search with no results shows a message', async ({ page }) => {
        await page.getByPlaceholder('Search domains or IP addresses...').fill('nomatch')
        await expect(cardTitles(page)).toHaveCount(0)
        await expect(page.getByText('No domains or endpoints found matching "nomatch"')).toBeVisible()
        await expectSummary(page, 0, 0, 0)
    })
})

test.describe('loading the status', () => {
    test('shows a loading indicator until the status arrives', async ({ page }) => {
        const response = deferred()
        await page.route('**/api/status', async (route) => {
            await response.promise
            await route.fulfill({ json: domains })
        })
        await page.goto('/')

        const loading = page.getByText('Loading domain data...')
        await expect(loading).toBeVisible()
        await expect(cardTitles(page)).toHaveCount(0)

        response.release()
        await expect(loading).toBeHidden()
        await expect(cardTitles(page)).toHaveText(domainNames)
    })

    test('shows a message when no domains are configured', async ({ page }) => {
        await mockStatus(page, {})
        await page.goto('/')

        await expect(page.getByText('No domains configured or available')).toBeVisible()
        await expect(page.getByTitle('Healthy domains', { exact: true })).toBeHidden()
    })

    test('shows an error when the status cannot be loaded', async ({ page }) => {
        await page.route('**/api/status', (route) => route.fulfill({ status: 500, body: 'boom' }))
        await page.goto('/')

        await expect(page.getByText(/^Failed to fetch domain data: HTTP error: 500/)).toBeVisible()
        await expect(cardTitles(page)).toHaveCount(0)
        await expect(page.getByTitle('Healthy domains', { exact: true })).toBeHidden()
        await expect(page.getByText('No domains configured or available')).toBeHidden()
    })
})

test.describe('refreshing', () => {
    test('auto-refresh reloads the status every minute until it is turned off', async ({ page }) => {
        await page.clock.install()
        const status = await mockStatus(page, domains)
        await page.goto('/')
        await expect(cardTitles(page)).toHaveText(domainNames)
        expect(status.requests).toBe(1)

        status.body = recoveredDomains
        await page.clock.fastForward('01:00')
        await expect.poll(() => status.requests).toBe(2)
        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Healthy')

        await page.getByRole('button', { name: 'Auto-refresh ON' }).click()
        const turnOn = page.getByRole('button', { name: 'Auto-refresh OFF' })
        await expect(turnOn).toBeVisible()
        await page.clock.fastForward('05:00')

        // Turning it back on loads the status right away
        // If the interval had kept running while off, there would be more requests
        await turnOn.click()
        await expect(page.getByRole('button', { name: 'Auto-refresh ON' })).toBeVisible()
        await expect.poll(() => status.requests).toBe(3)
        await expect(page.getByText('Loading domain data...')).toBeHidden()
        expect(status.requests).toBe(3)
    })

    test('Refresh now reloads the status', async ({ page }) => {
        const status = await mockStatus(page, domains)
        await page.goto('/')
        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Unhealthy')

        status.body = recoveredDomains
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()

        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Healthy')
        expect(status.requests).toBe(2)
    })
})

test.describe('Check now', () => {
    test('runs the health checks and shows the new status', async ({ page }) => {
        await mockStatus(page, domains)
        const response = deferred()
        const checkRequests: Request[] = []
        await page.route('**/api/check', async (route) => {
            checkRequests.push(route.request())
            await response.promise
            await route.fulfill({ json: recoveredDomains })
        })
        await page.goto('/')
        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Unhealthy')

        const checkNow = page.getByRole('button', { name: 'Check now' })
        await checkNow.click()

        // The button is disabled until the check completes
        await expect(page.getByRole('button', { name: 'Checking…' })).toBeDisabled()

        response.release()
        await expect(checkNow).toBeEnabled()
        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Healthy')

        // The server requires this header, which blocks cross-site requests
        expect(checkRequests).toHaveLength(1)
        expect(checkRequests[0].method()).toBe('POST')
        expect(checkRequests[0].headers()['x-requested-by']).toBe('ddup-dashboard')
    })

    test('shows an error when the check fails', async ({ page }) => {
        await mockStatus(page, domains)
        await page.route('**/api/check', (route) =>
            route.fulfill({
                status: 504,
                json: { code: 'api_check_timeout', message: 'The check did not complete in time' },
            })
        )
        await page.goto('/')
        await expect(cardTitles(page)).toHaveText(domainNames)

        const checkNow = page.getByRole('button', { name: 'Check now' })
        await checkNow.click()

        await expect(page.getByText(/^Failed to run check: HTTP error: 504/)).toBeVisible()
        await expect(checkNow).toBeEnabled()
    })
})

test.describe('status icon', () => {
    // The icon shows the worst status of all the domains
    const cases: { when: string; body: DomainsResponse; status: OverallStatus }[] = [
        {
            when: 'all the domains are healthy',
            body: pickDomains(domains, 'healthy.example', 'ipv6.example'),
            status: 'healthy',
        },
        {
            when: 'a domain has some unhealthy endpoints',
            body: pickDomains(domains, 'healthy.example', 'partial.example'),
            status: 'warning',
        },
        // This includes a domain with a warning too
        { when: 'a domain is unhealthy', body: domains, status: 'unhealthy' },
        { when: 'no domains are configured', body: {}, status: 'unknown' },
    ]
    for (const { when, body, status } of cases) {
        test(`is ${status} when ${when}`, async ({ page }) => {
            await mockStatus(page, body)
            await page.goto('/')
            await expect(page.getByText('Last updated:')).toBeVisible()

            await expectStatusIcon(page, status)
        })
    }

    test('is unknown until the status arrives', async ({ page }) => {
        const response = deferred()
        await page.route('**/api/status', async (route) => {
            await response.promise
            await route.fulfill({ json: domains })
        })
        await page.goto('/')
        await expect(page.getByText('Loading domain data...')).toBeVisible()

        await expectStatusIcon(page, 'unknown')

        response.release()
        await expect(cardTitles(page)).toHaveText(domainNames)
        await expectStatusIcon(page, 'unhealthy')
    })

    test('is unhealthy when the status cannot be loaded', async ({ page }) => {
        await page.route('**/api/status', (route) => route.fulfill({ status: 500, body: 'boom' }))
        await page.goto('/')
        await expect(page.getByText(/^Failed to fetch domain data/)).toBeVisible()

        await expectStatusIcon(page, 'unhealthy')
    })

    test('is unhealthy when Check now fails, until the status is loaded again', async ({ page }) => {
        await mockStatus(page, pickDomains(domains, 'healthy.example', 'ipv6.example'))
        await page.route('**/api/check', (route) =>
            route.fulfill({
                status: 504,
                json: { code: 'api_check_timeout', message: 'The check did not complete in time' },
            })
        )
        await page.goto('/')
        await expectStatusIcon(page, 'healthy')

        // The domains were healthy when they were last loaded, but the icon shows that something is wrong
        await page.getByRole('button', { name: 'Check now' }).click()
        await expect(page.getByText(/^Failed to run check/)).toBeVisible()
        await expectStatusIcon(page, 'unhealthy')

        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()
        await expect(cardTitles(page)).toHaveText(['healthy.example', 'ipv6.example'])
        await expectStatusIcon(page, 'healthy')
    })

    test('changes when the status changes', async ({ page }) => {
        const status = await mockStatus(page, pickDomains(domains, 'healthy.example', 'down.example'))
        await page.goto('/')
        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Unhealthy')
        await expectStatusIcon(page, 'unhealthy')

        status.body = pickDomains(recoveredDomains, 'healthy.example', 'down.example')
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()

        await expect(domainBadge(page, 'down.example')).toHaveAccessibleName('Healthy')
        await expectStatusIcon(page, 'healthy')
    })

    test('ignores the search', async ({ page }) => {
        await mockStatus(page, domains)
        await page.goto('/')
        await expect(cardTitles(page)).toHaveText(domainNames)

        await page.getByPlaceholder('Search domains or IP addresses...').fill('healthy.example')
        await expect(cardTitles(page)).toHaveText(['healthy.example'])
        await expectSummary(page, 1, 0, 0)

        await expectStatusIcon(page, 'unhealthy')
    })

    test.describe('without JavaScript', () => {
        test.use({ javaScriptEnabled: false })

        test('index.html has a static icon', async ({ page }) => {
            await page.goto('/')

            await expect(tabIcon(page)).toHaveAttribute('href', '/favicon.svg')
            const response = await page.request.get('/favicon.svg')
            expect(response.ok()).toBe(true)
            expect(response.headers()['content-type']).toBe('image/svg+xml')
        })
    })
})

test.describe('endpoint history', () => {
    // Fix the language, the time zone and the current date, so the times are formatted the same everywhere
    test.use({ locale: 'en-US', timezoneId: 'UTC' })

    test.beforeEach(async ({ page }) => {
        await page.clock.setFixedTime(new Date('2026-10-13T10:00:00Z'))
        await mockStatus(page, historyDomains)
        await page.goto('/')
        await expect(cardTitles(page)).toHaveText(['history.example', 'other.example'])
    })

    test('opens with a click and closes with the same button', async ({ page }) => {
        const button = historyButton(page, 'history.example')
        await expect(button).toHaveAttribute('type', 'button')
        await expect(button).toHaveAttribute('aria-haspopup', 'dialog')
        await expect(button).toHaveAttribute('aria-expanded', 'false')
        await expect(page.getByRole('dialog')).toHaveCount(0)

        await button.click()
        const dialog = historyDialog(page, 'history.example')
        await expect(dialog).toBeVisible()
        await expect(button).toHaveAttribute('aria-expanded', 'true')
        await expect(button).toHaveAttribute('aria-controls', (await dialog.getAttribute('id')) ?? '')
        // Focus moves into the dialog
        await expect(dialog).toBeFocused()

        await button.click()
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await expect(button).toHaveAttribute('aria-expanded', 'false')
        await expect(button).not.toHaveAttribute('aria-controls')
        await expect(button).toBeFocused()
    })

    for (const key of ['Enter', 'Space']) {
        test(`opens with the keyboard (${key})`, async ({ page }) => {
            const button = historyButton(page, 'history.example')
            await button.focus()
            await page.keyboard.press(key)

            const dialog = historyDialog(page, 'history.example')
            await expect(dialog).toBeVisible()
            await expect(dialog).toBeFocused()
            await expect(button).toHaveAttribute('aria-expanded', 'true')
        })
    }

    test('Escape closes it and focus returns to the button', async ({ page }) => {
        const button = historyButton(page, 'history.example')
        await button.click()
        await expect(historyDialog(page, 'history.example')).toBeFocused()

        await page.keyboard.press('Escape')
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await expect(button).toBeFocused()
        await expect(button).toHaveAttribute('aria-expanded', 'false')
    })

    test('has no close button', async ({ page }) => {
        await historyButton(page, 'history.example').click()
        await expect(historyDialog(page, 'history.example').getByRole('button')).toHaveCount(0)
    })

    test('a click outside closes it', async ({ page }) => {
        const button = historyButton(page, 'history.example')
        await button.click()
        await expect(historyDialog(page, 'history.example')).toBeVisible()

        // Clicking inside the dialog does not close it
        await historyDialog(page, 'history.example').getByText('Last updated:').click()
        await expect(historyDialog(page, 'history.example')).toBeVisible()

        await page.getByRole('heading', { name: 'ddup' }).click()
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await expect(button).toHaveAttribute('aria-expanded', 'false')
        // Focus doesn't stay on the body
        await expect(button).toBeFocused()
    })

    test('a click on another control closes it and that control gets focus', async ({ page }) => {
        await historyButton(page, 'history.example').click()
        await expect(historyDialog(page, 'history.example')).toBeVisible()

        const search = page.getByPlaceholder('Search domains or IP addresses...')
        await search.click()
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await expect(search).toBeFocused()
    })

    test('a tap outside closes it', async ({ browser }) => {
        const context = await browser.newContext({
            hasTouch: true,
            isMobile: true,
            locale: 'en-US',
            timezoneId: 'UTC',
            viewport: { width: 390, height: 844 },
        })
        const page = await context.newPage()
        await mockStatus(page, historyDomains)
        await page.goto('/')
        await expect(cardTitles(page)).toHaveText(['history.example', 'other.example'])

        await historyButton(page, 'history.example').tap()
        await expect(historyDialog(page, 'history.example')).toBeVisible()

        await page.getByRole('heading', { name: 'ddup' }).tap()
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await context.close()
    })

    test('moving focus out of it with Tab closes it, without a keyboard trap', async ({ page }) => {
        await historyButton(page, 'history.example').click()
        const dialog = historyDialog(page, 'history.example')
        await expect(dialog).toBeFocused()

        // There is nothing to focus inside the dialog, so the next stop is outside
        await page.keyboard.press('Tab')
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await expect(historyButton(page, 'history.example')).toHaveAttribute('aria-expanded', 'false')
    })

    test('moving focus out of it with Shift+Tab closes it', async ({ page }) => {
        const button = historyButton(page, 'history.example')
        await button.click()
        await expect(historyDialog(page, 'history.example')).toBeFocused()

        // The button is right before the dialog
        await page.keyboard.press('Shift+Tab')
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await expect(button).toBeFocused()
    })

    test('only one is open at a time', async ({ page }) => {
        await historyButton(page, 'history.example').click()
        await expect(historyDialog(page, 'history.example')).toBeVisible()

        await historyButton(page, 'other.example').click()
        await expect(page.getByRole('dialog')).toHaveCount(1)
        await expect(historyDialog(page, 'other.example')).toBeVisible()
        await expect(historyButton(page, 'history.example')).toHaveAttribute('aria-expanded', 'false')
        await expect(historyButton(page, 'other.example')).toHaveAttribute('aria-expanded', 'true')
    })

    test('lists the endpoints with how long they have been healthy or failing', async ({ page }) => {
        await historyButton(page, 'history.example').click()
        const items = historyDialog(page, 'history.example').getByRole('listitem')
        await expect(items).toHaveCount(6)

        // Recovered, with the last failure
        await expect(items.nth(0)).toContainText('192.0.2.10')
        await expect(items.nth(0)).toContainText('Healthy since Oct 9, 8:15:00 AM')
        await expect(items.nth(0)).toContainText('Last failure: Oct 9, 8:10:00 AM')

        // Failing
        await expect(items.nth(1)).toContainText('192.0.2.11')
        await expect(items.nth(1)).toContainText('Unhealthy since Oct 13, 9:00:00 AM')
        await expect(items.nth(1)).toContainText('3 failed checks in a row')
        await expect(items.nth(1)).toContainText('First failure: Oct 13, 9:00:00 AM')
        await expect(items.nth(1)).toContainText('Last failure: Oct 13, 9:29:00 AMconnection refused')

        // The start is not known
        await expect(items.nth(2)).toContainText('192.0.2.12')
        await expect(items.nth(2)).toContainText('Healthy since ddup started (Oct 13, 7:45:00 AM)')
        await expect(items.nth(2)).not.toContainText('Last failure')

        // No times from the server: only the state
        await expect(items.nth(3)).toContainText('192.0.2.13')
        await expect(items.nth(3)).toContainText('Healthy')
        await expect(items.nth(3)).not.toContainText('since')
        await expect(items.nth(3)).not.toContainText('Last failure')

        // Warning: healthy, with the failed checks in a row and the first and last failure
        await expect(items.nth(4)).toContainText('192.0.2.14')
        await expect(items.nth(4)).toContainText('Warning: 2 failed checks in a row')
        await expect(items.nth(4)).not.toContainText('Healthy since')
        await expect(items.nth(4)).toContainText('First failure: Oct 13, 9:27:00 AM')
        await expect(items.nth(4)).toContainText('Last failure: Oct 13, 9:28:00 AMstatus 503')

        // The last failure is above the first one
        const warningText = (await items.nth(4).innerText()).replace(/\s+/g, ' ')
        expect(warningText.indexOf('Last failure')).toBeGreaterThan(-1)
        expect(warningText.indexOf('Last failure')).toBeLessThan(warningText.indexOf('First failure'))

        // A single failed check is shown once
        await expect(items.nth(5)).toContainText('Warning: 1 failed check in a row')
        await expect(items.nth(5)).toContainText('Last failure: Oct 13, 9:28:30 AMstatus 500')
        await expect(items.nth(5)).not.toContainText('First failure')

        // The times are real time elements
        const first = items.nth(0).locator('time')
        await expect(first).toHaveCount(2)
        await expect(first.nth(0)).toHaveAttribute('datetime', '2026-10-09T08:15:00Z')
        await expect(first.nth(1)).toHaveAttribute('datetime', '2026-10-09T08:10:00Z')
    })

    test('includes the year for times before this year', async ({ page }) => {
        await page.unroute('**/api/status')
        await mockStatus(page, {
            'old.example': {
                lastUpdated: '2026-10-13T09:30:00Z',
                provider: 'ovh',
                endpoints: [{ healthy: true, ip: '198.51.100.8', since: '2025-12-31T23:30:00Z' }],
            },
        })
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()
        await expect(cardTitles(page)).toHaveText(['old.example'])

        await historyButton(page, 'old.example').click()
        await expect(historyDialog(page, 'old.example')).toContainText('Healthy since Dec 31, 2025, 11:30:00 PM')
    })

    test('the button is an icon at the end of the line with the name', async ({ page }) => {
        const card = domainCard(page, 'history.example')
        const button = card.getByRole('button', { name: 'History of history.example' })
        await expect(button).toBeVisible()
        await expect(button).toHaveText('')
        await expect(button).toHaveAttribute('title', 'Endpoint history')
        await expect(button.locator('svg')).toHaveCount(1)

        const buttonBox = (await button.boundingBox())!
        const titleBox = (await card.locator('[data-slot="card-title"]').boundingBox())!
        const headerBox = (await card.locator('[data-slot="card-header"]').boundingBox())!
        expect(buttonBox.x).toBeGreaterThan(titleBox.x + titleBox.width)
        // The header has a padding of 24 pixels
        expect(headerBox.x + headerBox.width - (buttonBox.x + buttonBox.width)).toBeLessThanOrEqual(24)
        expect(Math.abs(buttonBox.y + buttonBox.height / 2 - (titleBox.y + titleBox.height / 2))).toBeLessThan(4)

        // The line that counts the healthy endpoints has only the count
        await expect(card.getByText('5/6 endpoints healthy', { exact: true })).toBeVisible()

        // The time of the last update is only in the history dialog
        await expect(card.locator('time')).toHaveCount(0)
    })

    test('shows the time of the last update, with seconds, as the first line of the dialog', async ({ page }) => {
        await historyButton(page, 'history.example').click()
        const dialog = historyDialog(page, 'history.example')
        await expect(dialog).toContainText('Last updated: Oct 13, 9:30:00 AM')
        await expect(dialog.locator('time[datetime="2026-10-13T09:30:00Z"]')).toHaveCount(1)

        // The heading comes first, then the time
        const lines = await dialog.locator('h2, p').allInnerTexts()
        expect(lines[0]).toBe('Endpoint history of history.example')
        expect(lines[1]).toBe('Last updated: Oct 13, 9:30:00 AM')
    })

    test('the endpoint rows stay the same', async ({ page }) => {
        const card = domainCard(page, 'history.example')
        await expect(endpointRow(card, '192.0.2.11')).toContainText('Failures: 3')
        await expect(endpointRow(card, '192.0.2.10')).toContainText('Failures: 0')
        await expect(card).not.toContainText('since')
    })

    test('stays inside a narrow screen', async ({ page }) => {
        await page.setViewportSize({ width: 320, height: 640 })
        const pageWidth = await page.evaluate(() => document.documentElement.scrollWidth)
        for (const domain of ['history.example', 'other.example']) {
            await historyButton(page, domain).click()
            const dialog = historyDialog(page, domain)
            await expect(dialog).toBeVisible()

            const box = await dialog.boundingBox()
            expect(box).not.toBeNull()
            expect(box!.x).toBeGreaterThanOrEqual(0)
            expect(box!.y).toBeGreaterThanOrEqual(0)
            expect(box!.x + box!.width).toBeLessThanOrEqual(320)
            expect(box!.y + box!.height).toBeLessThanOrEqual(640)
            // The dialog doesn't make the page wider
            expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(pageWidth)

            await page.keyboard.press('Escape')
        }
    })

    test('scrolls with the page next to its button, and stays next to it when resized', async ({ page }) => {
        await page.setViewportSize({ width: 800, height: 400 })
        const button = historyButton(page, 'history.example')
        await button.click()
        const dialog = historyDialog(page, 'history.example')
        await expect(dialog).toBeVisible()

        // The dialog is right below the button, or right above it if there is more room there
        const gap = async () => {
            const b = (await button.boundingBox())!
            const d = (await dialog.boundingBox())!
            return Math.min(Math.abs(d.y - (b.y + b.height)), Math.abs(b.y - (d.y + d.height)))
        }
        await expect.poll(gap).toBeLessThan(10)

        // The dialog moves together with the button while the page scrolls, without catching up later
        // It also stays on the same side of the button, even if the other side has more room now
        const buttonHandle = await button.elementHandle()
        const offsets = await dialog.evaluate(async (el, buttonEl) => {
            const offset = () => {
                const d = el.getBoundingClientRect()
                const b = buttonEl!.getBoundingClientRect()
                return { x: d.left - b.left, y: d.top - b.top }
            }
            const nextFrame = () => new Promise((resolve) => requestAnimationFrame(resolve))

            const before = offset()
            const scrollBefore = window.scrollY
            window.scrollBy(0, 150)
            const scrolled = window.scrollY - scrollBefore
            const during = offset()
            await nextFrame()
            await nextFrame()
            return { before, during, after: offset(), scrolled }
        }, buttonHandle)
        expect(offsets.scrolled).toBeGreaterThan(100)
        expect(offsets.during).toEqual(offsets.before)
        expect(offsets.after).toEqual(offsets.before)

        await page.setViewportSize({ width: 360, height: 400 })
        await expect.poll(gap).toBeLessThan(10)
        await expect
            .poll(async () => {
                const box = (await dialog.boundingBox())!
                return box.x + box.width
            })
            .toBeLessThanOrEqual(360)
    })

    test('the button is at least 24 by 24 pixels', async ({ page }) => {
        const box = await historyButton(page, 'history.example').boundingBox()
        expect(box!.width).toBeGreaterThanOrEqual(24)
        expect(box!.height).toBeGreaterThanOrEqual(24)
    })

    test('the button shows a pointer cursor', async ({ page }) => {
        await expect(historyButton(page, 'history.example')).toHaveCSS('cursor', 'pointer')
    })

    test('shows the dialog in front of the next cards', async ({ page }) => {
        await page.setViewportSize({ width: 1280, height: 800 })
        await historyButton(page, 'history.example').click()
        const dialog = historyDialog(page, 'history.example')
        await expect(dialog).toBeVisible()

        // The element at the center of the dialog is the dialog (or something in it), not a card behind it
        const inFront = await dialog.evaluate((el) => {
            const rect = el.getBoundingClientRect()
            const top = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2)
            return top !== null && el.contains(top)
        })
        expect(inFront).toBe(true)
    })
})

test('enabled buttons show a pointer cursor', async ({ page }) => {
    await mockStatus(page, domains)
    await page.goto('/')
    await expect(cardTitles(page)).toHaveText(domainNames)

    // Open the menu so its items are included
    await page.getByRole('button', { name: 'More refresh options' }).click()
    await expect(page.getByRole('menuitem', { name: 'Refresh now' })).toBeVisible()

    const cursors = await page
        .locator('button:enabled, [role="button"]:not([aria-disabled="true"]), [role="menuitem"]:not([data-disabled])')
        .evaluateAll((elements) =>
            elements.map((el) => ({
                element: el.getAttribute('aria-label') || el.textContent?.trim(),
                cursor: getComputedStyle(el).cursor,
            }))
        )
    expect(cursors.length).toBeGreaterThan(0)
    expect(cursors).toEqual(cursors.map((c) => ({ ...c, cursor: 'pointer' })))
})
