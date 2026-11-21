package e2e

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// sortStrings sorts a slice of strings in place.
func sortStrings(s []string) { slices.Sort(s) }

// asError finds a *types.Error in err's chain and stores it in target.
func asError(err error, target **types.Error) bool { return errors.As(err, target) }

// writeFiles writes every file of a plan under dir, creating directories as needed. Paths
// use forward slashes, as Render returns them.
func writeFiles(dir string, files map[string][]byte) error {
	for path, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies the directory src to dst, file by file.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// ModuleRoot returns the directory of the netctl module. It is worked out from the location
// of this source file, so it does not depend on the working directory of the test, which a
// test may change.
func ModuleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate the module root")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", err
	}
	return root, nil
}
