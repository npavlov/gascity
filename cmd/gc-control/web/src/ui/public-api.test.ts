import ts from "typescript";
import { describe, expect, it } from "vitest";

import indexSource from "./index.ts?raw";
import readmeSource from "./README.md?raw";

const values = [
  "Button",
  "IconButton",
  "Input",
  "Select",
  "Textarea",
  "Text",
  "Stack",
  "Grid",
  "Badge",
  "StatusSignal",
  "Progress",
  "Tabs",
  "Tooltip",
  "Dialog",
  "Panel",
  "EmptyState",
  "Skeleton",
  "Spinner",
  "ActionBar",
  "DetailHeader",
  "ToolFrame",
  "ActivityIcon",
  "AlertIcon",
  "CheckIcon",
  "ChevronDownIcon",
  "ChevronLeftIcon",
  "ChevronRightIcon",
  "CloseIcon",
  "ContainerIcon",
  "DiffIcon",
  "ExternalLinkIcon",
  "InfoIcon",
  "LandmarkIcon",
  "ListChecksIcon",
  "MailIcon",
  "MessageIcon",
  "MoreIcon",
  "PauseIcon",
  "PlayIcon",
  "RefreshIcon",
  "SearchIcon",
  "StopIcon",
  "TerminalIcon",
  "TruckIcon",
].sort();

const types = [
  "ButtonProps",
  "IconButtonProps",
  "InputProps",
  "SelectProps",
  "TextareaProps",
  "TextProps",
  "StackProps",
  "GridProps",
  "BadgeProps",
  "StatusSignalProps",
  "ProgressProps",
  "TabsProps",
  "TooltipProps",
  "DialogProps",
  "PanelProps",
  "EmptyStateProps",
  "SkeletonProps",
  "SpinnerProps",
  "ActionBarProps",
  "DetailHeaderProps",
  "ToolFrameProps",
  "UIIcon",
  "ButtonVariant",
  "ButtonSize",
  "TextVariant",
  "Space",
  "StatusTone",
  "TabItem",
].sort();

function parseExports(source: string) {
  const file = ts.createSourceFile("index.ts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const foundValues: string[] = [];
  const foundTypes: string[] = [];
  file.statements.forEach((statement) => {
    if (!ts.isExportDeclaration(statement) || !statement.exportClause) return;
    if (!ts.isNamedExports(statement.exportClause)) return;
    statement.exportClause.elements.forEach((element) => {
      (statement.isTypeOnly || element.isTypeOnly ? foundTypes : foundValues).push(element.name.text);
    });
  });
  return { values: foundValues.sort(), types: foundTypes.sort() };
}

describe("@/ui public API", () => {
  it("exports exactly the approved values and types", () => {
    expect(parseExports(indexSource)).toEqual({ values, types });
    expect(indexSource).not.toMatch(/export\s+\*/);
    expect(indexSource).not.toMatch(/DynamicIcon|@radix-ui/);
  });

  it("documents every public export exactly once without domain imports", () => {
    const markers = [...readmeSource.matchAll(/<!--\s*@ui-export\s+([A-Za-z_$][\w$]*)\s*-->/g)].map(
      (match) => match[1],
    );
    expect(markers.sort()).toEqual([...values, ...types].sort());
    expect(new Set(markers).size).toBe(markers.length);
    expect(readmeSource).not.toMatch(/from\s+["'][^"']*(?:features|convoys|orders|sessions|mail)/);
  });
});
