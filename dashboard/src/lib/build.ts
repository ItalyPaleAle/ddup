// The build of the server that sent this page, and the reload when the server is upgraded
//
// The server writes its build info into the page (a JSON script element) and sends the identifier of its build in the X-Ddup-Build header of every API response
// After an upgrade, the page that is still open runs old code: the first API response with a different identifier makes it reload

export interface BuildInfo {
    // Identifier of the build, the same string the server sends in the header
    build: string
    version: string
    buildId?: string
    commit?: string
    buildDate?: string
}

const BUILD_ELEMENT_ID = 'ddup-build'
const BUILD_HEADER = 'X-Ddup-Build'
const GUARD_KEY = 'ddup-reloaded-for-build'
const GUARD_NAME_PREFIX = 'ddup-reloaded:'

// Reloads for the same server build: at most this many, at least this far apart
const MAX_ATTEMPTS = 3
const MIN_INTERVAL_MS = 30_000

// The build info in the page, or null if there is none (for example with the Vite dev server) or it's invalid
function readBuildInfo(): BuildInfo | null {
    try {
        const text = document.getElementById(BUILD_ELEMENT_ID)?.textContent
        if (!text) {
            return null
        }
        const info = JSON.parse(text) as Partial<BuildInfo> | null
        if (!info || typeof info.build !== 'string' || typeof info.version !== 'string') {
            return null
        }
        return info as BuildInfo
    } catch {
        return null
    }
}

// Read once, when the page loads
export const pageBuildInfo = readBuildInfo()

interface ReloadRecord {
    // The build of the server that the page was last reloaded for
    build: string
    // When the last reload for that build was done, in milliseconds since the epoch
    at: number
    // How many reloads were done for that build
    attempts: number
}

interface ReloadDecisionInput {
    // The build of the page, or null if the page has none
    pageBuild: string | null
    // The build in the header of the response, or null if there is none
    serverBuild: string | null
    // The last reload done, if known
    last: ReloadRecord | null
    now: number
}

// Decides if the page must reload because the server has a different build
// The reload can be answered by an old instance during a rolling update, so it's tried again for the same server build, but at most MAX_ATTEMPTS times and MIN_INTERVAL_MS apart
// This stops loops (for example when two versions answer in turn, or when a proxy caches the page)
export function shouldReload({ pageBuild, serverBuild, last, now }: ReloadDecisionInput): boolean {
    if (!pageBuild || !serverBuild || pageBuild === serverBuild) {
        return false
    }
    if (!last || last.build !== serverBuild) {
        return true
    }
    return last.attempts < MAX_ATTEMPTS && now - last.at >= MIN_INTERVAL_MS
}

function parseRecord(text: string | null): ReloadRecord | null {
    if (!text) {
        return null
    }
    try {
        const rec = JSON.parse(text) as Partial<ReloadRecord> | null
        if (rec && typeof rec.build === 'string' && typeof rec.at === 'number' && typeof rec.attempts === 'number') {
            return { build: rec.build, at: rec.at, attempts: rec.attempts }
        }
    } catch {
        // Ignore invalid records
    }
    return null
}

// Reads the last reload from the session storage
// If it's not available, window.name is used: unlike a variable it survives a reload of the page
function readRecord(): ReloadRecord | null {
    let stored: ReloadRecord | null = null
    try {
        stored = parseRecord(sessionStorage.getItem(GUARD_KEY))
    } catch {
        // Use window.name only
    }

    const named = window.name.startsWith(GUARD_NAME_PREFIX)
        ? parseRecord(window.name.slice(GUARD_NAME_PREFIX.length))
        : null
    return stored && named ? (stored.at >= named.at ? stored : named) : (stored ?? named)
}

function writeRecord(record: ReloadRecord): void {
    const text = JSON.stringify(record)
    try {
        sessionStorage.setItem(GUARD_KEY, text)
        return
    } catch {
        // Use window.name instead
    }
    window.name = GUARD_NAME_PREFIX + text
}

// Reloads the page if the response is from a server with a different build
export function checkServerBuild(response: Response): void {
    const serverBuild = response.headers.get(BUILD_HEADER)
    const last = readRecord()
    const now = Date.now()
    if (!shouldReload({ pageBuild: pageBuildInfo?.build ?? null, serverBuild, last, now })) {
        return
    }

    const attempts = last && last.build === serverBuild ? last.attempts + 1 : 1
    writeRecord({ build: serverBuild as string, at: now, attempts })
    window.location.reload()
}

// fetch for the API of the server, which checks the build of the server in the response
export async function apiFetch(input: string, init?: RequestInit): Promise<Response> {
    const response = await fetch(input, init)
    checkServerBuild(response)
    return response
}
