import eslint from "@eslint/js";
import jsxA11y from "eslint-plugin-jsx-a11y";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist/**", "src/generated/**", "**/fixtures/**"] },
  eslint.configs.recommended,
  {
    files: ["*.config.{js,mjs}", "scripts/**/*.mjs"],
    languageOptions: { globals: globals.node },
  },
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
      parserOptions: { ecmaFeatures: { jsx: true } },
    },
    plugins: {
      "jsx-a11y": jsxA11y,
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...jsxA11y.flatConfigs.recommended.rules,
      ...reactHooks.configs.flat.recommended.rules,
      "react-refresh/only-export-components": ["error", { allowConstantExport: true }],
    },
  },
  {
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/ui/**"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@/ui/*", "**/ui/*"],
              message: "Import Control Center UI only from @/ui.",
            },
          ],
        },
      ],
      "no-restricted-syntax": [
        "error",
        {
          selector: "JSXOpeningElement[name.name='button']",
          message: "Use Button or IconButton from @/ui.",
        },
        {
          selector: "JSXOpeningElement[name.name='input']",
          message: "Use Input from @/ui.",
        },
        {
          selector: "JSXOpeningElement[name.name='select']",
          message: "Use Select from @/ui.",
        },
        {
          selector: "JSXOpeningElement[name.name='textarea']",
          message: "Use Textarea from @/ui.",
        },
      ],
    },
  },
);
