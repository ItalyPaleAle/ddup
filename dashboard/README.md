# ddup dashboard

The web dashboard of ddup, a React app built with Vite and Tailwind CSS.
The ddup binary embeds the build output in `dist/`, so run `make dashboard` from the root of the repository after changing the dashboard.

To build, lint, and test the dashboard, see [Working on the dashboard](../DEVELOPMENT.md#working-on-the-dashboard).

## Icons

The icon of the browser tab shows the overall status with a badge in the corner (a check, a warning triangle, a cross, or a grey circle with a dash for unknown).
The tab icons are pre-rendered PNGs in `src/assets/favicons/` and the page swaps the icon for the current status.

All the shapes are defined in `src/lib/favicon-shapes.ts`.  
After changing them, regenerate the PNGs, `public/favicon.svg` (the icon without a badge) and `icon.png` in the root of the repository with:

```sh
pnpm run build:favicons
```

The script needs Node.js 24 and Chrome or Chromium: set `CHROME_BIN` if it's not in the PATH as `google-chrome`, `google-chrome-stable`, `chromium` or `chromium-browser`.
