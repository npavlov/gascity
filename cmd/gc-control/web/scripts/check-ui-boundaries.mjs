import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import ts from "typescript";

const scriptRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...(await filesUnder(target)));
    else if (/\.(?:ts|tsx)$/.test(entry.name) && !/\.(?:test|test-d)\./.test(entry.name)) {
      files.push(target);
    }
  }
  return files;
}

function parse(source, filename) {
  return ts.createSourceFile(filename, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
}

function exportedNames(sourceFile) {
  const names = [];
  for (const statement of sourceFile.statements) {
    if (!ts.isExportDeclaration(statement) || !statement.exportClause) continue;
    if (ts.isNamedExports(statement.exportClause)) {
      for (const element of statement.exportClause.elements) names.push(element.name.text);
    }
  }
  return names;
}

function importSpecifiers(sourceFile) {
  return sourceFile.statements
    .filter(ts.isImportDeclaration)
    .map((statement) => statement.moduleSpecifier)
    .filter(ts.isStringLiteral)
    .map((specifier) => specifier.text);
}

function resolveImport(specifier, fromFile, rootDir) {
  if (specifier.startsWith("@/")) return path.resolve(rootDir, "src", specifier.slice(2));
  if (specifier.startsWith(".")) return path.resolve(path.dirname(fromFile), specifier);
  return undefined;
}

export async function checkUIBoundaries({ rootDir = scriptRoot } = {}) {
  const uiDir = path.resolve(rootDir, "src/ui");
  const indexFile = path.join(uiDir, "index.ts");
  const readmeFile = path.join(uiDir, "README.md");
  const indexSource = await readFile(indexFile, "utf8");
  const readme = await readFile(readmeFile, "utf8");
  const exports = exportedNames(parse(indexSource, indexFile));
  const markers = [...readme.matchAll(/<!--\s*@ui-export\s+([A-Za-z_$][\w$]*)\s*-->/g)].map(
    (match) => match[1],
  );
  const exampleBlocks = [...readme.matchAll(/```(?:ts|tsx)\n([\s\S]*?)```/g)].map(
    (match) => match[1],
  );
  const examples = exampleBlocks.join("\n");
  const errors = [];

  for (const example of exampleBlocks) {
    const result = ts.transpileModule(example, {
      compilerOptions: {
        jsx: ts.JsxEmit.ReactJSX,
        module: ts.ModuleKind.ESNext,
        target: ts.ScriptTarget.ES2022,
      },
      fileName: "README-example.tsx",
      reportDiagnostics: true,
    });
    if (result.diagnostics?.some((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error)) {
      errors.push("README example does not compile");
    }
  }

  for (const name of new Set([...exports, ...markers])) {
    const exportCount = exports.filter((candidate) => candidate === name).length;
    const markerCount = markers.filter((candidate) => candidate === name).length;
    if (exportCount !== 1 || markerCount !== 1) {
      errors.push(`${name} must have one public export and one README marker`);
    }
    if (!new RegExp(`\\b${name}\\b`).test(examples)) errors.push(`${name} needs a compiling example`);
  }

  const sourceFiles = await filesUnder(path.join(rootDir, "src"));
  for (const filename of sourceFiles) {
    const source = await readFile(filename, "utf8");
    const parsed = parse(source, filename);
    for (const specifier of importSpecifiers(parsed)) {
      const resolved = resolveImport(specifier, filename, rootDir);
      if (resolved && !filename.startsWith(uiDir + path.sep) && resolved.startsWith(uiDir + path.sep)) {
        if (specifier !== "@/ui") errors.push(`${filename} must import Control Center UI only from @/ui`);
      }
      if (filename.startsWith(uiDir + path.sep) && resolved?.startsWith(path.join(rootDir, "src/features"))) {
        errors.push(`${filename} imports a feature-domain module`);
      }
    }
  }

  if (errors.length) throw new Error(errors.join("\n"));
  return { exports };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  checkUIBoundaries().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
