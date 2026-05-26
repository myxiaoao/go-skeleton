// shell_scripts_test.go 覆盖 scripts/*.sh 的关键失败语义。
package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runShellScript(t *testing.T, workdir, script string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(thisDir(t), script)}, args...)...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), env...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err == nil {
		return 0, out
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out
	}
	t.Fatalf("exec %s: %v\noutput:\n%s", script, err, out)
	return -1, out
}

func TestDevAllPropagatesServiceExitCode(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	writeFile(t, filepath.Join(dir, "go.mod"), "module go-skeleton\n")
	bashEnv := filepath.Join(dir, "fake-env.sh")
	writeFile(t, bashEnv, `make() {
  case " $* " in
    *" dev-deps-check "*) return 0 ;;
    *) return 0 ;;
  esac
}

go() {
  if [ "$1" = "run" ] && [ "$2" = "./cmd/migrate" ]; then return 0; fi
  if [ "$1" = "run" ] && [ "$2" = "./cmd/api" ]; then echo api boom; return 7; fi
  if [ "$1" = "run" ] && [ "$2" = "./cmd/worker" ]; then
    trap "exit 0" TERM
    while true; do sleep 1; done
  fi
  return 0
}
`)

	code, out := runShellScript(t, dir, "dev-all.sh", []string{
		"BASH_ENV=" + bashEnv,
		"GRACEFUL_SHUTDOWN_TIMEOUT=1",
	})
	if code != 7 {
		t.Fatalf("dev-all exit=%d, want 7\n%s", code, out)
	}
	for _, want := range []string{
		"[api]    api boom",
		"api exited (code=7)",
		"SIGTERM ->",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dev-all output missing %q:\n%s", want, out)
		}
	}
}

func TestRenameRejectsInvalidShortname(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, filepath.Join(dir, "go.mod"), "module go-skeleton\n")
	cmd := exec.Command("git", "add", "go.mod")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add go.mod: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "commit", "--quiet", "-m", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	code, out := runShellScript(t, dir, "rename.sh", nil,
		"github.com/acme/payments", "Payments_API")
	if code == 0 {
		t.Fatalf("rename should reject invalid shortname\n%s", out)
	}
	if !strings.Contains(out, "RFC 1123") {
		t.Errorf("expected RFC 1123 diagnostic, got:\n%s", out)
	}
}
