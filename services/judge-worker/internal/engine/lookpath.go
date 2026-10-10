package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func lookPath(prog string, env []string) (string, error) {
	path := "/usr/local/bin:/usr/bin:/bin"
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, prog)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New(prog + " not found")
}
