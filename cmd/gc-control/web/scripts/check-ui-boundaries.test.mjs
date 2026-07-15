import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { ESLint } from "eslint";
import stylelint from "stylelint";
import { describe, expect, it } from "vitest";

import { checkUIBoundaries } from "./check-ui-boundaries.mjs";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

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
    await mkdir(path.join(rootDir, "src/ui"), { recursive: true });
    await writeFile(path.join(rootDir, "src/ui/index.ts"), "const Button = 1; export { Button };\n");
    await writeFile(
      path.join(rootDir, "src/ui/README.md"),
      "<!-- @ui-export Button -->\n```ts\nconst Button = ;\n```\n",
    );

    await expect(checkUIBoundaries({ rootDir })).rejects.toThrow(/README example does not compile/);
  });
});
