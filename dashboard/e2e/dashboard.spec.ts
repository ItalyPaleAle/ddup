import { expect, test, type Locator, type Page, type Request } from '@playwright/test'

// Same shape as the response of the /api/status endpoint
interface DomainStatusEndpoint {
    healthy: boolean
    ip: string
    failureCount?: number
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
        await expect(domainBadge(page, 'healthy.example')).toHaveText('Healthy')
        await expect(domainBadge(page, 'ipv6.example')).toHaveText('Healthy')
        await expect(domainBadge(page, 'partial.example')).toHaveText('Warning')
        await expect(domainBadge(page, 'down.example')).toHaveText('Unhealthy')
        // A DNS error makes the domain unhealthy, even if its endpoints are healthy
        await expect(domainBadge(page, 'dns-error.example')).toHaveText('Unhealthy')
        await expect(domainBadge(page, 'no-endpoints.example')).toHaveText('Unhealthy')

        await expect(page.getByText('Last updated:')).toBeVisible()
    })

    test('shows the details of each domain', async ({ page }) => {
        const card = domainCard(page, 'partial.example')
        await expect(card).toContainText('Provider: ovh')
        await expect(card).toContainText('1/2 endpoints healthy')
        await expect(card).toContainText('Endpoints (2)')

        const healthyRow = endpointRow(card, '198.51.100.1')
        await expect(healthyRow).toContainText('Healthy')
        await expect(healthyRow).toContainText('Failures: 0')
        const unhealthyRow = endpointRow(card, '198.51.100.2')
        await expect(unhealthyRow).toContainText('Unhealthy')
        await expect(unhealthyRow).toContainText('Failures: 3')

        // Endpoints that are still healthy can have recent failures
        await expect(endpointRow(domainCard(page, 'healthy.example'), '192.0.2.2')).toContainText('Failures: 1')

        await expect(domainCard(page, 'dns-error.example')).toContainText('Domain Error')
        await expect(domainCard(page, 'dns-error.example')).toContainText('Error updating DNS records: boom')

        const emptyCard = domainCard(page, 'no-endpoints.example')
        await expect(emptyCard).toContainText('0/0 endpoints healthy')
        await expect(emptyCard).not.toContainText('Endpoints (')
    })

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
        await expect(domainBadge(page, 'down.example')).toHaveText('Healthy')

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
        await expect(domainBadge(page, 'down.example')).toHaveText('Unhealthy')

        status.body = recoveredDomains
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()

        await expect(domainBadge(page, 'down.example')).toHaveText('Healthy')
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
        await expect(domainBadge(page, 'down.example')).toHaveText('Unhealthy')

        const checkNow = page.getByRole('button', { name: 'Check now' })
        await checkNow.click()

        // The button is disabled until the check completes
        await expect(page.getByRole('button', { name: 'Checking…' })).toBeDisabled()

        response.release()
        await expect(checkNow).toBeEnabled()
        await expect(domainBadge(page, 'down.example')).toHaveText('Healthy')

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
        await expect(domainBadge(page, 'down.example')).toHaveText('Unhealthy')
        await expectStatusIcon(page, 'unhealthy')

        status.body = pickDomains(recoveredDomains, 'healthy.example', 'down.example')
        await page.getByRole('button', { name: 'More refresh options' }).click()
        await page.getByRole('menuitem', { name: 'Refresh now' }).click()

        await expect(domainBadge(page, 'down.example')).toHaveText('Healthy')
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
