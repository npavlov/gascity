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

function readProjectCompilerOptions(rootDir) {
  const configFile = ts.findConfigFile(rootDir, ts.sys.fileExists, "tsconfig.json");
  if (!configFile) throw new Error(`Control Center tsconfig.json is missing under ${rootDir}`);
  const config = ts.readConfigFile(configFile, ts.sys.readFile);
  if (config.error) {
    throw new Error(ts.flattenDiagnosticMessageText(config.error.messageText, "\n"));
  }
  const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, path.dirname(configFile));
  if (parsed.errors.length > 0) {
    throw new Error(
      parsed.errors.map((diagnostic) => ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n")).join("\n"),
    );
  }
  return {
    ...parsed.options,
    composite: false,
    incremental: false,
    noEmit: true,
    tsBuildInfoFile: undefined,
  };
}

function checkExampleSemantics(example, index, rootDir, compilerOptions) {
  const virtualFile = path.resolve(rootDir, `src/ui/__readme-example-${index}.tsx`);
  const host = ts.createCompilerHost(compilerOptions, true);
  const originalFileExists = host.fileExists.bind(host);
  const originalReadFile = host.readFile.bind(host);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  const isVirtual = (filename) => path.resolve(filename) === virtualFile;

  host.fileExists = (filename) => isVirtual(filename) || originalFileExists(filename);
  host.readFile = (filename) => (isVirtual(filename) ? example : originalReadFile(filename));
  host.getSourceFile = (filename, languageVersion, onError, shouldCreateNewSourceFile) =>
    isVirtual(filename)
      ? ts.createSourceFile(filename, example, languageVersion, true, ts.ScriptKind.TSX)
      : originalGetSourceFile(filename, languageVersion, onError, shouldCreateNewSourceFile);

  const program = ts.createProgram({
    rootNames: [virtualFile],
    options: compilerOptions,
    host,
  });
  return ts
    .getPreEmitDiagnostics(program)
    .filter((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error);
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
  const compilerOptions = readProjectCompilerOptions(rootDir);

  for (const [index, example] of exampleBlocks.entries()) {
    const diagnostics = checkExampleSemantics(example, index, rootDir, compilerOptions);
    if (diagnostics.length > 0) {
      const detail = diagnostics
        .slice(0, 3)
        .map((diagnostic) => ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n"))
        .join("; ");
      errors.push(`README example does not compile: ${detail}`);
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
