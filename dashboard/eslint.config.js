import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import prettier from 'eslint-plugin-prettier'
import prettierConfig from 'eslint-config-prettier'
import tseslint from '@typescript-eslint/eslint-plugin'
import tsparser from '@typescript-eslint/parser'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
    globalIgnores(['dist']),
    // JavaScript/JSX files
    {
        files: ['**/*.{js,jsx}'],
        extends: [
            js.configs.recommended,
            reactHooks.configs['recommended-latest'],
            reactRefresh.configs.vite,
            prettierConfig,
        ],
        plugins: {
            prettier,
        },
        languageOptions: {
            ecmaVersion: 2022,
            globals: globals.browser,
            parserOptions: {
                ecmaVersion: 'latest',
                ecmaFeatures: { jsx: true },
                sourceType: 'module',
            },
        },
        rules: {
            'no-unused-vars': ['error', { varsIgnorePattern: '^[A-Z_]' }],
            'prettier/prettier': 'error',
        },
    },
    // TypeScript/TSX files
    {
        files: ['**/*.{ts,tsx}'],
        languageOptions: {
            parser: tsparser,
            parserOptions: {
                ecmaVersion: 'latest',
                sourceType: 'module',
                ecmaFeatures: { jsx: true },
            },
            globals: globals.browser,
        },
        plugins: {
            '@typescript-eslint': tseslint,
            'react-hooks': reactHooks,
            'react-refresh': reactRefresh,
            prettier,
        },
        extends: [prettierConfig],
        rules: {
            ...tseslint.configs.recommended.rules,
            ...reactHooks.configs['recommended-latest'].rules,
            ...reactRefresh.configs.vite.rules,
            '@typescript-eslint/no-unused-vars': ['error', { varsIgnorePattern: '^[A-Z_]' }],
            'prettier/prettier': 'error',
        },
    },
])
