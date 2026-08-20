package git_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tinywasm/git"
)

// failingTransport fails every request: tests never touch the real network.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("no network in tests")
}

// newTestOAuth builds a GitHubOAuth with an in-memory store, an injected
// validator and an HTTP client that never leaves the machine.
func newTestOAuth(store git.SecretStore, validate func(string) git.TokenValidationResult) *git.GitHubOAuth {
	auth := git.NewGitHubOAuth()
	auth.SetStore(store)
	auth.SetTokenValidator(validate)
	auth.SetHTTPClient(&http.Client{Transport: failingTransport{}})
	return auth
}

// captureLog wires the auth logger into a strings.Builder and returns it.
func captureLog(auth *git.GitHubOAuth) *strings.Builder {
	var sb strings.Builder
	auth.SetLog(func(v ...any) {
		for _, s := range v {
			fmt.Fprint(&sb, s)
		}
	})
	return &sb
}

func TestTokenValidoNoLanzaDeviceFlow(t *testing.T) {
	withMockGH(t)
	headlessEnv(t)

	// gh sin sesión: se reconfigura con el token bueno y NO se pide device flow.
	authFile := filepath.Join(t.TempDir(), "authed")
	t.Setenv(mockAuthFileEnv, authFile)

	store := memStore{"github_token": "valid-pat"}
	auth := newTestOAuth(store, func(string) git.TokenValidationResult { return git.TokenValid })

	if err := auth.EnsureGitHubAuth(); err != nil {
		t.Fatalf("expected nil for a valid token, got: %v", err)
	}
	if tok, err := store.Get("github_token"); err != nil || tok != "valid-pat" {
		t.Fatalf("expected the valid token to survive, got %q err %v", tok, err)
	}
}

func TestGitHubRechazaElTokenSeBorraYSeReautentica(t *testing.T) {
	headlessEnv(t)

	store := memStore{"github_token": "stale-token"}
	auth := newTestOAuth(store, func(string) git.TokenValidationResult { return git.TokenRejected })

	// El device flow no puede completarse (transporte que falla): el test solo
	// comprueba que el token rechazado se borró y que el flujo siguió.
	err := auth.EnsureGitHubAuth()
	if err == nil {
		t.Fatal("expected the re-auth attempt to fail (failing transport)")
	}
	if _, err := store.Get("github_token"); err == nil {
		t.Fatal("expected the rejected token to be deleted from the store")
	}
}

func TestSinRedElTokenSobrevive(t *testing.T) {
	headlessEnv(t)

	store := memStore{"github_token": "precious-token"}
	auth := newTestOAuth(store, func(string) git.TokenValidationResult { return git.TokenUnverifiable })
	logs := captureLog(auth)

	err := auth.EnsureGitHubAuth()
	if err == nil {
		t.Fatal("expected an error when the token cannot be verified")
	}
	if tok, err := store.Get("github_token"); err != nil || tok != "precious-token" {
		t.Fatalf("expected the unverifiable token to survive, got %q err %v", tok, err)
	}
	if !strings.Contains(logs.String(), "no se pudo verificar") {
		t.Fatalf("expected the reason in the log, got: %q", logs.String())
	}
}

func TestNoSeReconfiguraGhSiYaTieneElMismoToken(t *testing.T) {
	withMockGH(t)
	headlessEnv(t)

	// gh ya tiene la sesión exactamente con este token.
	authFile := filepath.Join(t.TempDir(), "authed")
	if err := os.WriteFile(authFile, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(mockAuthFileEnv, authFile)
	loginLog := filepath.Join(t.TempDir(), "logins")
	t.Setenv(mockLoginLogEnv, loginLog)

	store := memStore{"github_token": "valid-pat"}
	auth := newTestOAuth(store, func(string) git.TokenValidationResult { return git.TokenValid })

	if err := auth.EnsureGitHubAuth(); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
	if tok, err := store.Get("github_token"); err != nil || tok != "valid-pat" {
		t.Fatalf("expected the token to survive, got %q err %v", tok, err)
	}
	if _, err := os.Stat(loginLog); err == nil {
		t.Fatal("expected gh auth login NOT to run when gh already has the same token")
	}
}

func TestCadaRamaRegistraSuMotivo(t *testing.T) {
	t.Run("tokenValidoPeroGhNoSePuedeConfigurar", func(t *testing.T) {
		withMockGH(t)
		headlessEnv(t)

		// gh sin sesión y el mock solo acepta "valid-pat": el login falla.
		authFile := filepath.Join(t.TempDir(), "authed")
		t.Setenv(mockAuthFileEnv, authFile)

		store := memStore{"github_token": "good-but-not-valid-pat"}
		auth := newTestOAuth(store, func(string) git.TokenValidationResult { return git.TokenValid })
		logs := captureLog(auth)

		if err := auth.EnsureGitHubAuth(); err != nil {
			t.Fatalf("expected nil (the token is fine), got: %v", err)
		}
		if !strings.Contains(logs.String(), "el token es válido pero no se pudo configurar gh") {
			t.Fatalf("expected the gh configuration warning in the log, got: %q", logs.String())
		}
	})

	t.Run("tokenRechazado", func(t *testing.T) {
		headlessEnv(t)

		store := memStore{"github_token": "stale"}
		auth := newTestOAuth(store, func(string) git.TokenValidationResult { return git.TokenRejected })
		logs := captureLog(auth)

		_ = auth.EnsureGitHubAuth()
		if !strings.Contains(logs.String(), "GitHub rechazó el token guardado") {
			t.Fatalf("expected the rejection reason in the log, got: %q", logs.String())
		}
	})

	t.Run("tokenInverificable", func(t *testing.T) {
		headlessEnv(t)

		store := memStore{"github_token": "precious"}
		auth := newTestOAuth(store, func(string) git.TokenValidationResult { return git.TokenUnverifiable })
		logs := captureLog(auth)

		_ = auth.EnsureGitHubAuth()
		if !strings.Contains(logs.String(), "no se pudo verificar el token de GitHub") {
			t.Fatalf("expected the unverifiable reason in the log, got: %q", logs.String())
		}
	})
}