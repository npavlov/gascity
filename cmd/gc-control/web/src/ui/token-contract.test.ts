import postcss from "postcss";
import { describe, expect, it } from "vitest";

import themesSource from "./themes.css?raw";
import tokensSource from "./tokens.css?raw";

const uiCSS = import.meta.glob("./**/*.css", {
  eager: true,
  import: "default",
  query: "?raw",
}) as Record<string, string>;

const sharedKeys = [
  ...["0", "1", "2", "3", "4", "5", "6", "8", "10", "12"].map(
    (key) => `--cc-space-${key}`,
  ),
  ...["sans", "mono"].map((key) => `--cc-font-family-${key}`),
  ...["xs", "sm", "md", "lg", "xl"].map((key) => `--cc-font-size-${key}`),
  ...["regular", "medium", "semibold"].map((key) => `--cc-font-weight-${key}`),
  ...["compact", "default", "relaxed"].map((key) => `--cc-font-line-${key}`),
  ...["compact", "regular"].map((key) => `--cc-size-control-${key}`),
  ...["sm", "md", "lg"].map((key) => `--cc-size-icon-${key}`),
  "--cc-size-border-default",
  ...["none", "sm", "md", "lg", "full"].map((key) => `--cc-radius-${key}`),
  ...["fast", "normal", "slow"].map((key) => `--cc-motion-duration-${key}`),
  "--cc-motion-ease-standard",
  ...["dropdown", "overlay", "dialog", "toast"].map((key) => `--cc-z-${key}`),
];

const requiredThemeKeys = [
  ...["canvas", "panel", "raised", "hover", "active", "overlay"].map(
    (key) => `--cc-color-surface-${key}`,
  ),
  ...["primary", "secondary", "muted", "inverse"].map(
    (key) => `--cc-color-text-${key}`,
  ),
  ...["subtle", "default", "strong", "focus"].map(
    (key) => `--cc-color-border-${key}`,
  ),
  ...["primary", "primary-hover", "primary-pressed", "on-primary"].map(
    (key) => `--cc-color-action-${key}`,
  ),
  ...["neutral", "info", "success", "warning", "danger"].flatMap((tone) =>
    ["foreground", "surface", "border"].map((key) => `--cc-color-${tone}-${key}`),
  ),
  "--cc-color-diff-added-foreground",
  "--cc-color-diff-added-surface",
  "--cc-color-diff-removed-foreground",
  "--cc-color-diff-removed-surface",
  "--cc-color-diff-context",
  "--cc-shadow-panel",
  "--cc-shadow-overlay",
  "--cc-shadow-focus",
];

function declarations(source: string) {
  const root = postcss.parse(source);
  const result = new Map<string, string[]>();
  root.walkRules((rule) => {
    const keys: string[] = [];
    rule.walkDecls(/^--cc-/, (decl) => {
      keys.push(decl.prop);
    });
    result.set(rule.selector, keys);
  });
  return { root, result };
}

describe("Control Center token contract", () => {
  it("defines each shared scale token exactly once and every namespace", () => {
    expect(tokensSource).toContain("--cc-space-0");
    expect(themesSource).toContain("--cc-color-surface-canvas");
    const tokenRoot = postcss.parse(tokensSource);
    const roots = [tokenRoot, postcss.parse(themesSource)];
    const allKeys: string[] = [];
    roots.forEach((root) => root.walkDecls(/^--cc-/, (decl) => {
      allKeys.push(decl.prop);
    }));

    expect(allKeys).toEqual(
      expect.arrayContaining(
        ["color", "space", "font", "size", "radius", "shadow", "motion", "z"].map(
          (namespace) => expect.stringMatching(new RegExp(`^--cc-${namespace}-`)),
        ),
      ),
    );
    const baseKeys: string[] = [];
    tokenRoot.nodes
      .filter((node) => node.type === "rule" && node.selector === ":root")
      .forEach((node) => {
        if (node.type === "rule") {
          node.walkDecls(/^--cc-/, (decl) => {
            baseKeys.push(decl.prop);
          });
        }
      });
    for (const key of sharedKeys) {
      expect(baseKeys.filter((candidate) => candidate === key), key).toHaveLength(1);
    }
  });

  it("keeps light and dark theme key sets identical and complete", () => {
    const { result } = declarations(themesSource);
    const light = result.get(":root,\n[data-theme=\"light\"]") ?? [];
    const dark = result.get("[data-theme=\"dark\"]") ?? [];

    expect([...dark].sort()).toEqual([...light].sort());
    expect(light).toEqual(expect.arrayContaining(requiredThemeKeys));
    expect(new Set(light).size).toBe(light.length);
    expect(new Set(dark).size).toBe(dark.length);
  });

  it("provides reduced-motion overrides for every duration", () => {
    const root = postcss.parse(tokensSource);
    const reduced = new Set<string>();
    root.walkAtRules("media", (media) => {
      if (media.params.includes("prefers-reduced-motion")) {
        media.walkDecls(/^--cc-motion-duration-/, (decl) => {
          reduced.add(decl.prop);
        });
      }
    });

    expect([...reduced]).toEqual(
      expect.arrayContaining([
        "--cc-motion-duration-fast",
        "--cc-motion-duration-normal",
        "--cc-motion-duration-slow",
      ]),
    );
  });

  it("uses a nonzero semantic width for visible borders and dividers", () => {
    const tokens = postcss.parse(tokensSource);
    const borderToken: string[] = [];
    tokens.walkDecls("--cc-size-border-default", (decl) => {
      borderToken.push(decl.value);
    });
    expect(borderToken).toEqual(["1px"]);

    const zeroWidthBorders: string[] = [];
    for (const [filename, source] of Object.entries(uiCSS)) {
      postcss.parse(source).walkDecls(/^border(?:-(?:top|right|bottom|left))?$/, (decl) => {
        if (decl.value.includes("solid") && decl.value.includes("var(--cc-space-0)")) {
          zeroWidthBorders.push(`${filename}:${decl.prop}`);
        }
      });
    }
    expect(zeroWidthBorders).toEqual([]);
  });
});
