package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tinywasm/command"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DevflowOAuthClientID is the OAuth App Client ID for devflow.
//
// IMPORTANT: This Client ID is intentionally hardcoded and is NOT a secret.
// OAuth Client IDs are public identifiers (like a username, not a password).
// The Client Secret is NEVER included in the code - Device Flow doesn't need it.
// This is the standard approach used by CLI tools like gh, goreleaser, hub, etc.
//
// The OAuth App is registered under a personal GitHub account (not organization).
// Manage the app at: https://github.com/settings/developers -> OAuth Apps -> devflow
const DevflowOAuthClientID = "Ov23lijHU2vxBCpShn1Q"

// GitHub token key for SecretStore storage
const githubTokenKey = "github_token"

// TokenValidationResult distingue los tres desenlaces posibles de la
// validación de un token contra la API de GitHub. Sin esta distinción,
// cualquier tropiezo de una herramienta externa se interpretaba como
// "credencial inválida" y borraba el token.
type TokenValidationResult uint8

const (
	TokenValid           TokenValidationResult = iota // GitHub aceptó el token
	TokenRejected                                     // GitHub devolvió 401: el token ya no sirve
	TokenUnverifiable                                 // no se pudo comprobar: red, gh, llavero…
)

const githubAPIUserURL = "https://api.github.com/user"

// tokenValidationTimeout es el timeout de la validación de tokens contra la API.
const tokenValidationTimeout = 10 * time.Second

// Motivos exactos de cada rama del flujo: nada de literales sueltos en la lógica.
const (
	// logGhConfigFailed avisa cuando GitHub aceptó el token pero gh no pudo
	// adoptarlo (llavero de gh bloqueado, PATH incompleto, sin red…).
	logGhConfigFailed = "aviso: el token es válido pero no se pudo configurar gh: "

	// logTokenRejected anuncia que la credencial guardada ya no sirve.
	logTokenRejected = "GitHub rechazó el token guardado (401): hay que autenticarse de nuevo"

	// tokenUnverifiableMessage avisa y se conserva el token: puede ser
	// perfectamente bueno.
	tokenUnverifiableMessage = "no se pudo verificar el token de GitHub (¿sin red?): se conserva el guardado; reintenta o borra con …"
)

// GitHubOAuth handles GitHub authentication and token management via Device Flow
type GitHubOAuth struct {
	log      func(...any)
	store    SecretStore
	validate func(token string) TokenValidationResult // nil ⇒ la implementación real contra la API
	client   *http.Client                             // nil ⇒ cliente por defecto con timeout
}

// NewGitHubOAuth creates a new GitHub authentication handler
func NewGitHubOAuth() *GitHubOAuth {
	return &GitHubOAuth{
		log: func(...any) {},
	}
}

// SetStore injects the SecretStore used to persist the OAuth token.
// Without a store, EnsureGitHubAuth and DeviceFlowAuth report a clear error
// (they can only authenticate interactively if they can store the result).
func (a *GitHubOAuth) SetStore(store SecretStore) {
	if store != nil {
		a.store = store
	}
}

// SetTokenValidator overrides how a stored token is validated. Tests inject a
// fake validator so no real GitHub API call happens; nil (the default) uses
// the real check against the GitHub API.
func (a *GitHubOAuth) SetTokenValidator(fn func(token string) TokenValidationResult) {
	if fn != nil {
		a.validate = fn
	}
}

// SetHTTPClient overrides the HTTP client used for GitHub API calls. Tests
// inject a client that never leaves the machine; nil (the default) uses a
// client with a 30s timeout.
func (a *GitHubOAuth) SetHTTPClient(client *http.Client) {
	if client != nil {
		a.client = client
	}
}

// Name returns the handler name for TUI display.
func (a *GitHubOAuth) Name() string {
	return "GitHub Auth"
}

// SetLog sets the logger function
func (a *GitHubOAuth) SetLog(fn func(...any)) {
	if fn != nil {
		a.log = fn
	}
}

// deviceCodeResponse represents the response from GitHub's device code endpoint
type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// tokenResponse represents the response from GitHub's token endpoint
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// EnsureGitHubAuth checks if GitHub is authenticated via the SecretStore, and if not, initiates Device Flow
func (a *GitHubOAuth) EnsureGitHubAuth() error {
	if a.store == nil {
		return fmt.Errorf("github auth needs a SecretStore: inject one with SetStore (e.g. github.com/tinywasm/keyring) or set GH_TOKEN")
	}

	// Try to load saved token from the store
	token, err := a.store.Get(githubTokenKey)
	if err == nil && token != "" {
		switch a.validateToken(token) {
		case TokenValid:
			if err := a.ensureGhSessionMatches(token); err != nil {
				a.log(logGhConfigFailed + err.Error())
			}
			return nil

		case TokenRejected:
			a.log(logTokenRejected)
			a.store.Delete(githubTokenKey)
			// sigue al device flow

		case TokenUnverifiable:
			// NO se borra nada. El token puede ser perfectamente bueno.
			a.log(tokenUnverifiableMessage)
			return errors.New(tokenUnverifiableMessage)
		}
	}

	// Not authenticated - initiate Device Flow
	token, err = a.DeviceFlowAuth()
	if err != nil {
		return err
	}

	// Configure gh CLI with the new token
	return a.configureGhWithToken(token)
}

// validateToken pregunta a GitHub directamente. Es la ÚNICA autoridad sobre si
// un token sirve: gh puede fallar por su propia configuración sin que el token
// tenga nada que ver.
func (a *GitHubOAuth) validateToken(token string) TokenValidationResult {
	if a.validate != nil {
		return a.validate(token)
	}

	req, err := http.NewRequest("GET", githubAPIUserURL, nil)
	if err != nil {
		return TokenUnverifiable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := a.client
	if client == nil {
		client = &http.Client{Timeout: tokenValidationTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return TokenUnverifiable
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		return TokenValid
	case http.StatusUnauthorized:
		return TokenRejected
	default:
		return TokenUnverifiable
	}
}

// ensureGhSessionMatches configura gh SOLO si su token activo no es ya éste.
// gh auth login --with-token reescribe la configuración del usuario; hacerlo en
// cada arranque es una mutación gratuita y una oportunidad de fallo por arranque.
func (a *GitHubOAuth) ensureGhSessionMatches(token string) error {
	current, err := command.Run("gh", "auth", "token")
	if err == nil && strings.TrimSpace(current) == token {
		return nil
	}
	return a.configureGhWithToken(token)
}

// DeviceFlowAuth initiates GitHub OAuth Device Flow and returns an access token
func (a *GitHubOAuth) DeviceFlowAuth() (string, error) {
	if a.store == nil {
		return "", fmt.Errorf("github auth needs a SecretStore: inject one with SetStore (e.g. github.com/tinywasm/keyring) or set GH_TOKEN")
	}

	// Step 1: Request device and user codes
	codeResp, err := a.requestDeviceCode()
	if err != nil {
		return "", fmt.Errorf("failed to request device code: %w", err)
	}

	// Step 2: Open browser for user authorization
	// Use LogOpen prefix for animated progress in TUI
	a.log("[...", fmt.Sprintf("Paste this code in browser: %s", codeResp.UserCode))

	if err := a.openBrowser(codeResp.VerificationURI); err != nil {
		a.log(fmt.Sprintf("Could not open browser. Please go to: %s", codeResp.VerificationURI))
	}

	// Step 3: Poll for the access token
	interval := codeResp.Interval
	if interval < 5 {
		interval = 5
	}

	token, err := a.pollForToken(codeResp.DeviceCode, interval, codeResp.ExpiresIn)
	if err != nil {
		return "", err
	}

	// Step 4: Save token to the store
	if err := a.store.Set(githubTokenKey, token); err != nil {
		a.log(fmt.Sprintf("Warning: could not save token: %v", err))
	}

	// Use LogClose prefix to stop animation and show success
	a.log("...]", "GitHub authentication successful!")
	return token, nil
}

// httpClient devuelve el cliente inyectado con SetHTTPClient, o uno nuevo con
// el timeout por defecto cuando no se inyectó ninguno.
func (a *GitHubOAuth) httpClient() *http.Client {
	if a.client != nil {
		return a.client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// requestDeviceCode requests a device code from GitHub
func (a *GitHubOAuth) requestDeviceCode() (*deviceCodeResponse, error) {
	data := url.Values{}
	data.Set("client_id", DevflowOAuthClientID)
	data.Set("scope", "repo read:org delete_repo")

	req, err := http.NewRequest("POST", "https://github.com/login/device/code", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := a.httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var codeResp deviceCodeResponse
	if err := json.Unmarshal(body, &codeResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, string(body))
	}

	if codeResp.DeviceCode == "" {
		return nil, fmt.Errorf("no device code in response: %s", string(body))
	}

	return &codeResp, nil
}

// pollForToken polls GitHub for the access token
func (a *GitHubOAuth) pollForToken(deviceCode string, interval, expiresIn int) (string, error) {
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)

		data := url.Values{}
		data.Set("client_id", DevflowOAuthClientID)
		data.Set("device_code", deviceCode)
		data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

		req, err := http.NewRequest("POST", "https://github.com/login/oauth/access_token", strings.NewReader(data.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		client := a.httpClient()
		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		var tokenResp tokenResponse
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			continue
		}

		switch tokenResp.Error {
		case "":
			if tokenResp.AccessToken != "" {
				return tokenResp.AccessToken, nil
			}
		case "authorization_pending":
			// TUI handles animation, no need to log dots
			continue
		case "slow_down":
			interval += 5
			continue
		case "expired_token":
			return "", fmt.Errorf("authorization expired, please try again")
		case "access_denied":
			return "", fmt.Errorf("access denied by user")
		default:
			return "", fmt.Errorf("authorization failed: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
		}
	}

	return "", fmt.Errorf("authorization timed out")
}

// openBrowser opens a URL in the default browser (cross-platform)
func (a *GitHubOAuth) openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}

// configureGhWithToken configures gh CLI to use the token. The token travels
// by stdin, never as a process argument (an argv is visible in the process
// table and in error messages).
func (a *GitHubOAuth) configureGhWithToken(token string) error {
	_, err := command.RunWithStdin(token, "gh", "auth", "login", "--with-token")
	return err
}
