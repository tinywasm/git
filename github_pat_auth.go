package git

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const ghTokenKey = "GH_TOKEN"

// GitHubPATAuth manages the GitHub PAT via an injected SecretStore.
// It is used to recover the gh CLI session non-interactively.
type GitHubPATAuth struct {
	store SecretStore
	log   func(...any)
}

// NewGitHubPATAuth creates a GitHubPATAuth over the given store.
// The store may be nil: the auth flow then falls back to the GH_TOKEN
// environment variable and reports a clear error when neither exists.
func NewGitHubPATAuth(store SecretStore) *GitHubPATAuth {
	return &GitHubPATAuth{
		store: store,
		log:   func(...any) {},
	}
}

// SetLog sets the logging function.
func (a *GitHubPATAuth) SetLog(fn func(...any)) {
	if fn != nil {
		a.log = fn
	}
}

// HasToken returns true if the GitHub PAT is already stored in the environment or SecretStore.
func (a *GitHubPATAuth) HasToken() bool {
	if os.Getenv("GH_TOKEN") != "" {
		return true
	}
	if a.store == nil {
		return false
	}
	tok, err := a.store.Get(ghTokenKey)
	return err == nil && tok != ""
}

// EnsureToken returns the PAT from the environment or SecretStore; if absent, prompts once and persists.
func (a *GitHubPATAuth) EnsureToken() (string, error) {
	if envTok := os.Getenv("GH_TOKEN"); envTok != "" {
		return envTok, nil
	}

	if a.store == nil {
		return "", fmt.Errorf("no SecretStore configured and GH_TOKEN env var is not set")
	}

	tok, err := a.store.Get(ghTokenKey)
	if err == nil && tok != "" {
		return tok, nil
	}

	fmt.Fprintf(os.Stderr,
		"GitHub token not found. Create a fine-grained PAT (Contents + Pull requests: Read/Write) at %s\nEnter it now: ",
		termLink("https://github.com/settings/tokens", "https://github.com/settings/tokens"))

	tok, err = readSecret()
	if err != nil {
		return "", err
	}

	if tok == "" {
		return "", fmt.Errorf("no GitHub token provided")
	}

	if err := a.store.Set(ghTokenKey, tok); err != nil {
		a.log(fmt.Sprintf("warning: could not save GitHub token to store: %v", err))
	}

	return tok, nil
}

// Reset removes the GitHub PAT from the SecretStore.
func (a *GitHubPATAuth) Reset() error {
	if a.store == nil {
		return fmt.Errorf("no SecretStore configured")
	}
	return a.store.Delete(ghTokenKey)
}

// EnsureGitHubAuth fulfills the GitHubAuthenticator interface.
func (a *GitHubPATAuth) EnsureGitHubAuth() error {
	return EnsureGHSession(RealRunner{}, a.store)
}

// termLink returns an OSC 8 terminal hyperlink (supported by most modern terminals).
func termLink(text, url string) string {
	return fmt.Sprintf("\x1b]8;;%s\x1b\\\\%s\x1b]8;;\x1b\\\\", url, text)
}

// readSecret reads a secret from stdin without echoing.
func readSecret() (string, error) {
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("could not read secret: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}