package git

import "strings"

// WorkTreeDirtyBeyond returns true if the git worktree has changes beyond the allowed files.
// It ignores .env and .gitignore files automatically.
func WorkTreeDirtyBeyond(git GitClient, allowed ...string) (bool, error) {
	out, err := git.StatusPorcelain()
	if err != nil {
		return false, err
	}

	if out == "" {
		return false, nil
	}

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}

		// git status --porcelain output:
		// XY PATH
		// XY is 2 characters
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		file := strings.TrimSpace(parts[1])
		// If the file is quoted, unquote it (simplistic version)
		file = strings.Trim(file, "\"")
		if file == "" {
			continue
		}

		// Always ignore .env and .gitignore
		if file == ".env" || file == ".gitignore" {
			continue
		}

		// Check if it's in the allowed list
		isAllowed := false
		for _, a := range allowed {
			if file == a {
				isAllowed = true
				break
			}
		}

		if !isAllowed {
			return true, nil
		}
	}

	return false, nil
}