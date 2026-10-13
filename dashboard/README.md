# React + Vite

This template provides a minimal setup to get React working in Vite with HMR and some ESLint rules.

Currently, two official plugins are available:

- [@vitejs/plugin-react](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react) uses [Babel](https://babeljs.io/) for Fast Refresh
- [@vitejs/plugin-react-swc](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react-swc) uses [SWC](https://swc.rs/) for Fast Refresh

## Expanding the ESLint configuration

If you are developing a production application, we recommend using TypeScript with type-aware lint rules enabled. Check out the [TS template](https://github.com/vitejs/vite/tree/main/packages/create-vite/template-react-ts) for information on how to integrate TypeScript and [`typescript-eslint`](https://typescript-eslint.io) in your project.

## Icons

The icon of the browser tab shows the overall status with a badge in the corner (a check, a warning triangle, a cross, or a grey circle with a dash for unknown).
The tab icons are pre-rendered PNGs in `src/assets/favicons/`, so nothing is drawn at runtime: the page only swaps the icon for the current status.

All the shapes are defined in `src/lib/favicon-shapes.ts`.
After changing them, regenerate the PNGs, `public/favicon.svg` (the icon without a badge) and `icon.png` in the root of the repository with:

```sh
pnpm run build:favicons
```

The script needs Node.js 24 and Chrome or Chromium: set `CHROME_BIN` if it's not in the PATH as `google-chrome`, `chromium` or `chromium-browser`.
The normal build doesn't need it, because the generated files are committed.
