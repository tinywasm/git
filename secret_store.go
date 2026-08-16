package git

// SecretStore abstracts credential persistence for the GitHub auth flows.
//
// tinywasm/git deliberately depends on no concrete backend: inject the store
// that fits your environment — the OS keyring (github.com/tinywasm/keyring)
// for interactive CLIs, an in-memory map for tests/CI, or no store at all when
// GH_TOKEN is set. The auth flows degrade gracefully when store is nil.
type SecretStore interface {
	// Set stores value under key.
	Set(key, value string) error
	// Get returns the value stored under key.
	Get(key string) (string, error)
	// Delete removes key.
	Delete(key string) error
}