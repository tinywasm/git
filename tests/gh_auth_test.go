package git_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tinywasm/command"
	"github.com/tinywasm/git"
)

const mockAuthFileEnv = "MOCK_GH_AUTH_FILE"

// mockExecCommand simulates command execution for testing.
// The mock gh session lives in a temp file (MOCK_GH_AUTH_FILE) so parent and
// child processes observe the same state; `gh auth login --with-token` gets
// the PAT piped to its stdin, exactly like the real restore flow.
func mockExecCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=TestHelperProcess", "--", name}, args...)...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if name == "gh" && len(args) >= 3 && args[0] == "auth" && args[1] == "login" && args[2] == "--with-token" {
		cmd.Stdin = strings.NewReader("valid-pat")
	}
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(0)
	}

	switch args[0] {
	case "gh":
		handleGH(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown mock command: %s", args[0])
		os.Exit(1)
	}
}

// ghMockAuthed reports whether the mock gh session is authenticated,
// consulting the shared auth marker file.
func ghMockAuthed() bool {
	authFile := os.Getenv(mockAuthFileEnv)
	if authFile == "" {
		return false
	}
	_, err := os.Stat(authFile)
	return err == nil
}

func handleGH(args []string) {
	if len(args) == 0 {
		os.Exit(0)
	}

	switch args[0] {
	case "api":
		// gh api user --jq .login
		if len(args) >= 2 && args[1] == "user" {
			if !ghMockAuthed() {
				fmt.Fprintln(os.Stderr, "error: not authenticated")
				os.Exit(1)
			}
			fmt.Println("testuser")
			os.Exit(0)
		}
	case "auth":
		// gh auth login --with-token
		if len(args) >= 3 && args[1] == "login" && args[2] == "--with-token" {
			var token string
			fmt.Scanln(&token)
			if token == "valid-pat" {
				if f, err := os.Create(os.Getenv(mockAuthFileEnv)); err == nil {
					f.Close()
				}
				os.Exit(0)
			}
			fmt.Fprintln(os.Stderr, "error: invalid token")
			os.Exit(1)
		}
	}
	os.Exit(0)
}

// withMockGH replaces command.Exec with the gh mock for the test duration.
func withMockGH(t *testing.T) {
	t.Helper()
	oldExec := command.Exec
	command.Exec = mockExecCommand
	t.Cleanup(func() { command.Exec = oldExec })
}

// headlessEnv makes the tests deterministic across environments: no CI
// short-circuit, no real GH_TOKEN leaking into the auth flows.
func headlessEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("GH_TOKEN", "")
}

func TestEnsureGHSession_Healthy(t *testing.T) {
	withMockGH(t)
	headlessEnv(t)

	authFile := filepath.Join(t.TempDir(), "authed")
	if err := os.WriteFile(authFile, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(mockAuthFileEnv, authFile)

	if err := git.EnsureGHSession(git.RealRunner{}, memStore{}); err != nil {
		t.Fatalf("expected no error for healthy session, got: %v", err)
	}
}

func TestEnsureGHSession_RecoversFromStoredPAT(t *testing.T) {
	withMockGH(t)
	headlessEnv(t)

	// Session expired: the auth marker file does not exist.
	authFile := filepath.Join(t.TempDir(), "authed")
	t.Setenv(mockAuthFileEnv, authFile)

	store := memStore{"GH_TOKEN": "valid-pat"}
	if err := git.EnsureGHSession(git.RealRunner{}, store); err != nil {
		t.Fatalf("expected session recovery from the stored PAT, got: %v", err)
	}

	if _, err := os.Stat(authFile); err != nil {
		t.Fatal("expected the gh session to be restored (auth marker created)")
	}
}

func TestEnsureGHSession_InvalidStoredPAT(t *testing.T) {
	withMockGH(t)
	headlessEnv(t)

	authFile := filepath.Join(t.TempDir(), "authed")
	t.Setenv(mockAuthFileEnv, authFile)

	store := memStore{"GH_TOKEN": "wrong-pat"}
	if err := git.EnsureGHSession(git.RealRunner{}, store); err == nil {
		t.Fatal("expected an error for an invalid stored PAT")
	}
}