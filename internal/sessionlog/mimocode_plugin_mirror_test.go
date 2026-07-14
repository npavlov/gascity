package sessionlog

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/bootstrap/packs/core"
)

// mimoCodePluginPackPath is the embedded MiMo Code plugin location inside the
// core pack, the same file the hooks installer materializes into
// {workDir}/.mimocode/plugin/gascity.js.
const mimoCodePluginPackPath = "overlay/per-provider/mimocode/.mimocode/plugin/gascity.js"

// mimoCodeMirrorSegments is the home-relative mirror directory shared by the
// plugin's default transcript mirror and DefaultMimoCodeSearchPaths.
var mimoCodeMirrorSegments = []string{".local", "share", "gascity", "mimocode-transcripts"}

func resolveNodeExecutable(nodeOnPath string) (string, error) {
	out, err := exec.Command(nodeOnPath, "-p", "process.execPath").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("query node executable through %q: %w (output: %s)", nodeOnPath, err, strings.TrimSpace(string(out)))
	}

	nodeBin := strings.TrimSpace(string(out))
	if !filepath.IsAbs(nodeBin) {
		return "", fmt.Errorf("node process.execPath returned non-absolute path %q", nodeBin)
	}
	if _, err := exec.LookPath(nodeBin); err != nil {
		return "", fmt.Errorf("validate node executable %q: %w", nodeBin, err)
	}
	return nodeBin, nil
}

// TestMimoCodePluginDefaultMirrorDirMatchesReaderSearchPath pins the embedded
// MiMo Code plugin's default transcript mirror directory to the directory
// DefaultMimoCodeSearchPaths searches. A default-resolved mimocode session —
// no GC_MIMOCODE_TRANSCRIPT_DIR in the provider env — must mirror transcripts
// to a location `gc session log` discovery actually reads; if either side
// renames the directory without the other, this test fails.
func TestMimoCodePluginDefaultMirrorDirMatchesReaderSearchPath(t *testing.T) {
	plugin, err := fs.ReadFile(core.PackFS, mimoCodePluginPackPath)
	if err != nil {
		t.Fatalf("read embedded MiMo Code plugin: %v", err)
	}
	content := string(plugin)

	if !strings.Contains(content, "process.env.GC_MIMOCODE_TRANSCRIPT_DIR || defaultTranscriptDir()") {
		t.Fatalf("plugin must default the mirror dir when GC_MIMOCODE_TRANSCRIPT_DIR is absent:\n%s", content)
	}
	jsJoin := `path.join(home, "` + strings.Join(mimoCodeMirrorSegments, `", "`) + `")`
	if !strings.Contains(content, jsJoin) {
		t.Fatalf("plugin default mirror join %q not found:\n%s", jsJoin, content)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(append([]string{home}, mimoCodeMirrorSegments...)...)
	got := DefaultMimoCodeSearchPaths()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("DefaultMimoCodeSearchPaths() = %v, want [%q]", got, want)
	}
}

// TestMimoCodePluginMirrorsTranscriptByDefault executes the embedded plugin's
// event hook under node with no GC_MIMOCODE_TRANSCRIPT_DIR in the environment
// and proves a default-resolved mimocode session mirrors its transcript where
// the production reader discovers it: the plugin writes the export into
// DefaultMimoCodeSearchPaths' directory, FindMimoCodeSessionFile finds it for
// the session's work directory, and ReadMimoCodeFile parses it.
func TestMimoCodePluginMirrorsTranscriptByDefault(t *testing.T) {
	nodeOnPath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; cannot execute the MiMo Code plugin")
	}
	nodeBin, err := resolveNodeExecutable(nodeOnPath)
	if err != nil {
		t.Fatalf("resolve real node executable: %v", err)
	}

	plugin, err := fs.ReadFile(core.PackFS, mimoCodePluginPackPath)
	if err != nil {
		t.Fatalf("read embedded MiMo Code plugin: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := filepath.Join(home, "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}

	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "gascity.js"), plugin, 0o644); err != nil {
		t.Fatalf("stage plugin: %v", err)
	}
	// The mimo CLI loads workdir plugins as ES modules; a module-type
	// package.json gives the staged copy the same module semantics.
	if err := os.WriteFile(filepath.Join(stage, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatalf("stage package.json: %v", err)
	}
	driver := `import { pathToFileURL } from "node:url";

const [pluginPath, directory, sessionID] = process.argv.slice(2);
const { default: gascityPlugin } = await import(pathToFileURL(pluginPath).href);

const client = {
  session: {
    get: async () => ({ data: { id: sessionID, directory } }),
    messages: async () => ({
      data: [
        {
          info: {
            id: "msg_user_1",
            sessionID,
            role: "user",
            time: { created: 1770000000000 },
          },
          parts: [
            {
              id: "part_user_1",
              sessionID,
              messageID: "msg_user_1",
              type: "text",
              text: "hello default mirror",
            },
          ],
        },
      ],
    }),
  },
};

const hooks = await gascityPlugin({ directory, client });
await hooks.event({ event: { type: "session.idle", properties: { sessionID } } });
`
	driverPath := filepath.Join(stage, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatalf("stage driver: %v", err)
	}

	const sessionID = "ses_default_mirror"
	cmd := exec.Command(nodeBin, driverPath, filepath.Join(stage, "gascity.js"), workDir, sessionID)
	// Hermetic env: HOME drives the plugin's default mirror directory, and
	// GC_MIMOCODE_TRANSCRIPT_DIR is deliberately absent.
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node plugin driver: %v\noutput:\n%s", err, out)
	}

	paths := DefaultMimoCodeSearchPaths()
	if len(paths) != 1 {
		t.Fatalf("DefaultMimoCodeSearchPaths() = %v, want exactly one entry", paths)
	}
	mirror := filepath.Join(paths[0], sessionID+".json")
	if _, err := os.Stat(mirror); err != nil {
		t.Fatalf("default mirror file missing: %v\nnode output:\n%s", err, out)
	}

	found := FindMimoCodeSessionFile(nil, workDir)
	if found != mirror {
		t.Fatalf("FindMimoCodeSessionFile() = %q, want %q", found, mirror)
	}
	sess, err := ReadMimoCodeFile(found, 0)
	if err != nil {
		t.Fatalf("ReadMimoCodeFile: %v", err)
	}
	if sess.ID != sessionID {
		t.Fatalf("session ID = %q, want %q", sess.ID, sessionID)
	}
	if len(sess.Messages) != 1 || sess.Messages[0].TextContent() != "hello default mirror" {
		t.Fatalf("mirrored messages = %+v, want one user message %q", sess.Messages, "hello default mirror")
	}
}

func TestResolveNodeExecutableBypassesHomeDependentShim(t *testing.T) {
	lookupHome := t.TempDir()
	hermeticHome := t.TempDir()
	binDir := t.TempDir()
	realNode := filepath.Join(binDir, "real-node")
	if err := os.WriteFile(realNode, []byte("#!/bin/sh\nprintf 'real-node\\n'\n"), 0o755); err != nil {
		t.Fatalf("write real node fixture: %v", err)
	}

	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, "node")
	shimScript := `#!/bin/sh
if [ "$HOME" != "$TEST_NODE_LOOKUP_HOME" ]; then
  echo "node shim requires its original HOME" >&2
  exit 70
fi
if [ "$1" != "-p" ] || [ "$2" != "process.execPath" ]; then
  echo "node shim only supports executable discovery" >&2
  exit 71
fi
printf '%s\n' "$TEST_REAL_NODE"
`
	if err := os.WriteFile(shim, []byte(shimScript), 0o755); err != nil {
		t.Fatalf("write node shim fixture: %v", err)
	}

	t.Setenv("HOME", lookupHome)
	t.Setenv("PATH", shimDir)
	t.Setenv("TEST_NODE_LOOKUP_HOME", lookupHome)
	t.Setenv("TEST_REAL_NODE", realNode)

	nodeOnPath, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("look up node shim: %v", err)
	}
	resolved, err := resolveNodeExecutable(nodeOnPath)
	if err != nil {
		t.Fatalf("resolve node executable: %v", err)
	}
	cmd := exec.Command(resolved)
	cmd.Env = []string{
		"HOME=" + hermeticHome,
		"PATH=" + shimDir,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolved node executable %q cannot run with hermetic HOME: %v\noutput:\n%s", resolved, err, out)
	}
	if resolved != realNode {
		t.Fatalf("resolveNodeExecutable() = %q, want real executable %q", resolved, realNode)
	}
	if got := strings.TrimSpace(string(out)); got != "real-node" {
		t.Fatalf("real node fixture output = %q, want %q", got, "real-node")
	}
}
