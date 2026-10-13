import healthy from '@/assets/favicons/healthy.png?no-inline'
import warning from '@/assets/favicons/warning.png?no-inline'
import unhealthy from '@/assets/favicons/unhealthy.png?no-inline'
import unknown from '@/assets/favicons/unknown.png?no-inline'
import type { OverallStatus } from '@/lib/favicon-shapes'

// The icons are pre-rendered by scripts/build-favicons.mjs and bundled as separate cacheable files (not inlined), so nothing is drawn at runtime
const faviconUrls: Record<OverallStatus, string> = { healthy, warning, unhealthy, unknown }

// Updates the icon of the tab
export const setFavicon = (status: OverallStatus): void => {
    // Replace all the icons, so the browser can't keep using the static one
    const links = document.querySelectorAll<HTMLLinkElement>("link[rel~='icon']")
    const link = links[0] ?? document.head.appendChild(document.createElement('link'))
    links.forEach((el) => el !== link && el.remove())
    link.rel = 'icon'
    link.type = 'image/png'
    link.href = faviconUrls[status]
}
