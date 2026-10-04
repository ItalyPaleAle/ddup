import { useCallback, useEffect, useState } from 'react'

export type Theme = 'light' | 'dark' | 'system'

const storageKey = 'ddup-theme'

// Storage can be unavailable (private windows, blocked cookies), and the dashboard must work without it
export const getStoredTheme = (): Theme => {
    try {
        const value = localStorage.getItem(storageKey)
        if (value === 'light' || value === 'dark') {
            return value
        }
    } catch {
        // Ignore
    }
    return 'system'
}

const storeTheme = (theme: Theme): void => {
    try {
        if (theme === 'system') {
            localStorage.removeItem(storageKey)
        } else {
            localStorage.setItem(storageKey, theme)
        }
    } catch {
        // Ignore
    }
}

const systemQuery = '(prefers-color-scheme: dark)'

// Whether the dark theme is used, for a theme setting
const isDark = (theme: Theme): boolean =>
    theme === 'system' ? window.matchMedia(systemQuery).matches : theme === 'dark'

// Applies the theme to the page. index.html does the same before the page renders, to avoid a flash of the wrong theme
export const applyTheme = (theme: Theme): void => {
    document.documentElement.classList.toggle('dark', isDark(theme))
}

// The theme setting (light, dark, or following the system), kept in sync with the page and the system setting
export const useTheme = (): { theme: Theme; setTheme: (theme: Theme) => void } => {
    const [theme, setThemeState] = useState<Theme>(getStoredTheme)

    useEffect(() => {
        applyTheme(theme)
        if (theme !== 'system') {
            return
        }

        // Follow changes of the system setting
        const media = window.matchMedia(systemQuery)
        const onChange = () => applyTheme('system')
        media.addEventListener('change', onChange)
        return () => media.removeEventListener('change', onChange)
    }, [theme])

    const setTheme = useCallback((value: Theme) => {
        storeTheme(value)
        setThemeState(value)
    }, [])

    return { theme, setTheme }
}
