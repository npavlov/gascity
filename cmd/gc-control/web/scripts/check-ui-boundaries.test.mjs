import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { ESLint } from "eslint";
import stylelint from "stylelint";
import { describe, expect, it } from "vitest";

import { checkUIBoundaries } from "./check-ui-boundaries.mjs";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

async function writeTestTSConfig(rootDir) {
  await writeFile(
    path.join(rootDir, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        baseUrl: ".",
        module: "ESNext",
        moduleResolution: "Bundler",
        paths: { "@/*": ["src/*"] },
        strict: true,
        target: "ES2022",
      },
    }),
  );
}

describe("feature UI boundaries", () => {
  const eslintCases = [
    ['import { Badge } from "@/ui/components/Badge";', 1],
    ['import { Badge } from "../../ui/components/Badge";', 1],
    ["export function X() { return <button>Run</button>; }", 1],
    ['export function X() { return <input aria-label="Query" />; }', 1],
    ['export function X() { return <select aria-label="State" />; }', 1],
    ['export function X() { return <textarea aria-label="Message" />; }', 1],
    [
      'import { Button, StatusSignal } from "@/ui"; export function X() { return <Button><StatusSignal label="Running" /></Button>; }',
      0,
    ],
  ];

  it.each(eslintCases)("enforces the public barrel for feature source %#", async (code, count) => {
    const eslint = new ESLint({ cwd: webRoot });
    const [result] = await eslint.lintText(code, { filePath: "src/features/fixture.tsx" });
    expect(
      result.messages.filter((message) =>
        ["no-restricted-imports", "no-restricted-syntax"].includes(message.ruleId ?? ""),
      ),
    ).toHaveLength(count);
  });

  it("allows native controls inside UI implementations", async () => {
    const eslint = new ESLint({ cwd: webRoot });
    const [result] = await eslint.lintText(
      "export function NativeControls() { return <><button>Run</button><input /><select /><textarea /></>; }",
      { filePath: "src/ui/primitives/fixture.tsx" },
    );
    expect(
      result.messages.filter((message) =>
        ["no-restricted-imports", "no-restricted-syntax"].includes(message.ruleId ?? ""),
      ),
    ).toHaveLength(0);
  });

  const cssCases = [
    ["color: #fff", "src/features/x.css", true],
    ["background: rgb(0 0 0 / 50%)", "src/features/x.css", true],
    ["padding: 12px", "src/features/x.css", true],
    ["gap: 0.75rem", "src/features/x.css", true],
    ["border-radius: 6px", "src/features/x.css", true],
    ["transition-duration: 150ms", "src/features/x.css", true],
    ["color: var(--cc-color-text-primary)", "src/features/x.css", false],
    ["padding: var(--cc-space-3)", "src/features/x.css", false],
    ["--cc-space-3: 0.75rem", "src/ui/tokens.css", false],
    ["--cc-color-text-primary: #17202a", "src/ui/themes.css", false],
  ];

  it.each(cssCases)("enforces semantic CSS values for %s", async (declaration, filename, bad) => {
    const report = await stylelint.lint({
      code: `.fixture { ${declaration}; }`,
      codeFilename: path.join(webRoot, filename),
      configFile: path.join(webRoot, "stylelint.config.mjs"),
    });
    const warnings = report.results.flatMap((result) => result.warnings);
    expect(warnings.length > 0).toBe(bad);
  });

  it("rejects a relative feature import that resolves under src/ui", async () => {
    const rootDir = await mkdtemp(path.join(tmpdir(), "cc-ui-boundary-"));
    await writeTestTSConfig(rootDir);
    await mkdir(path.join(rootDir, "src/ui/components"), { recursive: true });
    await mkdir(path.join(rootDir, "src/features/demo"), { recursive: true });
    await writeFile(path.join(rootDir, "src/ui/index.ts"), "const Button = 1; export { Button };\n");
    await writeFile(path.join(rootDir, "src/ui/README.md"), "<!-- @ui-export Button -->\n```ts\nButton;\n```\n");
    await writeFile(path.join(rootDir, "src/ui/components/Badge.tsx"), "export const Badge = 1;\n");
    await writeFile(
      path.join(rootDir, "src/features/demo/view.tsx"),
      'import { Badge } from "../../ui/components/Badge"; void Badge;\n',
    );

    await expect(checkUIBoundaries({ rootDir })).rejects.toThrow(/only from @\/ui/);
  });

  it("rejects README examples that do not compile", async () => {
    const rootDir = await mkdtemp(path.join(tmpdir(), "cc-ui-readme-"));
    await writeTestTSConfig(rootDir);
    await mkdir(path.join(rootDir, "src/ui"), { recursive: true });
    await writeFile(path.join(rootDir, "src/ui/index.ts"), "const Button = 1; export { Button };\n");
    await writeFile(
      path.join(rootDir, "src/ui/README.md"),
      "<!-- @ui-export Button -->\n```ts\nconst Button = ;\n```\n",
    );

    await expect(checkUIBoundaries({ rootDir })).rejects.toThrow(/README example does not compile/);
  });

  it("rejects README TSX examples with missing @/ui exports and type errors", async () => {
    const rootDir = await mkdtemp(path.join(tmpdir(), "cc-ui-readme-types-"));
    await mkdir(path.join(rootDir, "src/ui"), { recursive: true });
    await writeTestTSConfig(rootDir);
    await writeFile(
      path.join(rootDir, "src/ui/index.ts"),
      'function Button(_props: { variant?: "primary" }) {} export { Button };\n',
    );
    await writeFile(
      path.join(rootDir, "src/ui/README.md"),
      '<!-- @ui-export Button -->\n```tsx\nimport { Button, Missing } from "@/ui";\nconst invalid: Parameters<typeof Button>[0] = { variant: "invalid" };\nvoid Missing;\nvoid invalid;\n```\n',
    );

    await expect(checkUIBoundaries({ rootDir })).rejects.toThrow(/README example does not compile/);
  });
});
