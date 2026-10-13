// The geometry of the icon, in a 64x64 box
// This file is pure data with no DOM use: the header icon, the script that pre-renders the PNGs and public/favicon.svg all come from here, so the shapes are defined only once

// The health shown in the browser tab and in the header: the worst status of all the domains
export type OverallStatus = 'healthy' | 'warning' | 'unhealthy' | 'unknown'

export const overallStatuses: readonly OverallStatus[] = ['healthy', 'warning', 'unhealthy', 'unknown']

// One shape of the icon
export type IconLayer = {
    d: string
    fill?: string
    stroke?: string
    strokeWidth?: number
}

const tile = '#1e293b'
const white = '#fff'

const badgeColors: Record<OverallStatus, string> = {
    healthy: '#22c55e',
    warning: '#eab308',
    unhealthy: '#ef4444',
    unknown: '#9ca3af',
}

const circle = (cx: number, cy: number, r: number): string =>
    `M${cx - r} ${cy}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0z`

// The icon without a badge: a "d" (a ring and a stem) on a rounded square
export const iconLayers: IconLayer[] = [
    { d: 'M14 0h36a14 14 0 0 1 14 14v36a14 14 0 0 1-14 14h-36a14 14 0 0 1-14-14v-36a14 14 0 0 1 14-14z', fill: tile },
    { d: circle(26, 34, 13), stroke: white, strokeWidth: 7 },
    { d: 'M39.5 10a3.5 3.5 0 0 1 3.5 3.5v31a3.5 3.5 0 0 1-7 0v-31a3.5 3.5 0 0 1 3.5-3.5z', fill: white },
]

// The badge in the corner has a different shape for each status, so the status doesn't depend on the color alone
// It has an outline in the color of the tile so it stands out from the icon
const trianglePath = 'M43 29L57 55H29z'
export const badgeLayers = (status: OverallStatus): IconLayer[] => {
    const color = badgeColors[status]
    switch (status) {
        case 'healthy':
            // A circle with a check
            return [
                { d: circle(45, 45, 15), fill: color, stroke: tile, strokeWidth: 5 },
                { d: circle(45, 45, 15), fill: color },
                { d: 'M38 45.5l5 5 9-10', stroke: white, strokeWidth: 4.5 },
            ]
        case 'warning':
            // A triangle with an exclamation mark
            return [
                { d: trianglePath, fill: tile, stroke: tile, strokeWidth: 7 },
                { d: trianglePath, fill: color, stroke: color, strokeWidth: 3 },
                { d: 'M43 38v7', stroke: tile, strokeWidth: 4.5 },
                { d: circle(43, 50, 2.4), fill: tile },
            ]
        case 'unhealthy':
            // A circle with a cross
            return [
                { d: circle(45, 45, 15), fill: color, stroke: tile, strokeWidth: 5 },
                { d: circle(45, 45, 15), fill: color },
                { d: 'M39 39l12 12M51 39l-12 12', stroke: white, strokeWidth: 4.5 },
            ]
        default:
            // A grey circle with a dash
            return [
                { d: circle(45, 45, 15), fill: color, stroke: tile, strokeWidth: 5 },
                { d: circle(45, 45, 15), fill: color },
                { d: 'M39 45h12', stroke: tile, strokeWidth: 4.5 },
            ]
    }
}

// The layers of the icon, with the badge of the status or without any badge when the status is omitted
export const layersFor = (status?: OverallStatus): IconLayer[] => [
    ...iconLayers,
    ...(status ? badgeLayers(status) : []),
]

// The same layers as the markup of a standalone SVG file
export const svgMarkup = (status?: OverallStatus): string => {
    const paths = layersFor(status).map((layer) => {
        const attrs = [`d="${layer.d}"`, `fill="${layer.fill ?? 'none'}"`]
        if (layer.stroke) {
            attrs.push(`stroke="${layer.stroke}"`, `stroke-width="${layer.strokeWidth ?? 1}"`)
        }
        return `<path ${attrs.join(' ')}/>`
    })
    return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" stroke-linecap="round" stroke-linejoin="round">${paths.join('')}</svg>\n`
}
