# git
<img src="docs/img/badges.svg">

Git and GitHub handlers for TinyWasm projects. Extracted from `tinywasm/devflow` so
consumers (e.g. `tinywasm/sitepub`) don't drag the whole devflow dependency tree.

## Features

- **Git handler** (`NewGit`): add/commit/tag/push, clone/pull/fetch, tags, `.gitignore` entries.
- **GitHub client** (`NewGitHub`): repos, releases, gh CLI integration.
- **GitHub auth**: OAuth Device Flow (`NewGitHubOAuth`), PAT recovery (`NewGitHubPATAuth`), `EnsureGHSession`.
- **Keyring** (`NewKeyring`): secure token storage via the system keyring.
- **Secrets** (`GitHubSecrets`): repository secrets via gh CLI.
- **TestCache** (`NewTestCache`): git-based test cache used by `gotest`.
- **Helpers**: `CompareVersions`, commit message builders, `WorkTreeDirtyBeyond`, publish objector types (`PublishContext`, `PublishAction`).

## Usage

```go
import "github.com/tinywasm/git"

g, err := git.NewGit()
if err != nil {
    log.Fatal(err)
}
```

## Installing tools

Use `goinstall` to (re)build the TinyWasm CLI tools from `tinywasm/devflow`.

## License

MIT