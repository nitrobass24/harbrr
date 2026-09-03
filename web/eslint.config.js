import js from "@eslint/js"
import stylistic from "@stylistic/eslint-plugin"
import reactHooks from "eslint-plugin-react-hooks"
import reactRefresh from "eslint-plugin-react-refresh"
import { globalIgnores } from "eslint/config"
import globals from "globals"
import tseslint from "typescript-eslint"

export default tseslint.config([
  globalIgnores(["dist", "src/routeTree.gen.ts", "src/types/api.gen.ts", "vite.config.ts"]),
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      // Type-aware tier: unsafe any-flows, floating/misused promises, etc.
      // (a deliberate step beyond qui's plain `recommended`).
      tseslint.configs.recommendedTypeChecked,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    plugins: {
      "@stylistic": stylistic,
      "react-hooks": reactHooks,
    },
    rules: {
      "@stylistic/quotes": ["warn", "double"],
      "@stylistic/comma-dangle": [
        "warn",
        {
          arrays: "always-multiline",
          objects: "always-multiline",
          imports: "never",
          exports: "always-multiline",
          functions: "never",
        },
      ],
      "@stylistic/indent": ["error", 2, { "SwitchCase": 1 }],
      "@stylistic/multiline-ternary": ["warn", "never"],
      "@stylistic/no-trailing-spaces": ["warn"],
      "@stylistic/object-curly-spacing": ["error", "always"],
      "@typescript-eslint/no-unused-vars": ["warn"],
      "@typescript-eslint/no-explicit-any": "error",
      "linebreak-style": ["error", "unix"],
      // Toasts go through lib/notify so error/warn toasts also relay to the daemon
      // log (harbrr#112) — direct sonner imports silently skip that relay.
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "sonner",
              message: "Use notify* from @/lib/notify — it is the one sanctioned sonner call site (error/warn toasts relay to the daemon log).",
            },
          ],
        },
      ],
      "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
    },
  },
  {
    // TanStack Router file routes export the Route object beside the component
    // by design, and shadcn/ui files export their cva variants the same way, so
    // the fast-refresh purity rule cannot hold in either tree.
    files: ["src/routes/**/*.tsx", "src/components/ui/**/*.tsx"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
  {
    // The two sanctioned sonner import sites: lib/notify.ts (the one toast() call
    // site, which relays error/warn toasts to the daemon log) and ui/sonner.tsx
    // (mounts sonner's <Toaster/>, never calls toast()).
    files: ["src/lib/notify.ts", "src/components/ui/sonner.tsx"],
    rules: {
      "no-restricted-imports": "off",
    },
  },
])
