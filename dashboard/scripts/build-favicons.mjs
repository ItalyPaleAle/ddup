#!/usr/bin/env node
// Pre-renders the icons from the geometry in src/lib/favicon-shapes.ts
//
// - src/assets/favicons/{healthy,warning,unhealthy,unknown}.png: the tab icons, one for each status, 64x64 (the browser scales a single size to what it needs)
// - public/favicon.svg: the icon without a badge, used as the static fallback in index.html
// - ../icon.png: the icon without a badge, 512x512 with a transparent background outside the rounded tile, used in the README
//
// The PNGs are committed, so the normal build doesn't need this: run it with "pnpm run build:favicons" only after changing the shapes
// It needs Chrome or Chromium: set CHROME_BIN, or have google-chrome, chromium or chromium-browser in the PATH
// Node.js 24 is needed to import the TypeScript file without a flag
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { overallStatuses, svgMarkup } from '../src/lib/favicon-shapes.ts'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

const findChrome = () => {
    const candidates = [process.env.CHROME_BIN, 'google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser']
    for (const candidate of candidates.filter(Boolean)) {
        try {
            execFileSync(candidate, ['--version'], { stdio: 'ignore' })
            return candidate
        } catch {
            // Try the next one
        }
    }
    console.error('Could not find Chrome or Chromium: set the CHROME_BIN environment variable to its path')
    process.exit(1)
}

const chrome = findChrome()
const tmp = mkdtempSync(path.join(tmpdir(), 'ddup-favicons-'))

// Renders the SVG markup to a PNG of size x size pixels, with a transparent background
const render = (svg, size, outFile) => {
    const page = path.join(tmp, 'page.html')
    writeFileSync(
        page,
        `<!doctype html><html><head><style>html,body{margin:0;background:transparent}svg{display:block;width:${size}px;height:${size}px}</style></head><body>${svg}</body></html>`
    )
    mkdirSync(path.dirname(outFile), { recursive: true })
    execFileSync(
        chrome,
        [
            '--headless=new',
            '--disable-gpu',
            '--hide-scrollbars',
            '--no-sandbox',
            '--force-device-scale-factor=1',
            '--default-background-color=00000000',
            `--window-size=${size},${size}`,
            `--screenshot=${outFile}`,
            `file://${page}`,
        ],
        { stdio: 'ignore' }
    )
}

try {
    for (const status of overallStatuses) {
        render(svgMarkup(status), 64, path.join(root, 'src/assets/favicons', `${status}.png`))
    }
    render(svgMarkup(), 512, path.join(root, '../icon.png'))
    writeFileSync(path.join(root, 'public/favicon.svg'), svgMarkup())
} finally {
    rmSync(tmp, { recursive: true, force: true })
}
