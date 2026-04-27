package deprecate

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

const (
	// RemovedVersion is the placeholder for the deprecation removal message.
	RemovedVersion = "v<NEXT_MINOR>"
)

// Message returns the canonical deprecation warning payload.
func Message(item, replacement string) string {
	return fmt.Sprintf("[deprecated] '%s' will be removed in %s; use '%s'", item, RemovedVersion, replacement)
}

// Warn prints a deprecation warning to stderr.
func Warn(item, replacement string) {
	fmt.Fprintln(os.Stderr, Message(item, replacement))
}

// EnsureMutuallyExclusiveStringFlags validates that at most one of the given flags
// is explicitly set.
func EnsureMutuallyExclusiveStringFlags(fs *pflag.FlagSet, names ...string) error {
	set := make([]string, 0, len(names))
	for _, name := range names {
		flag := fs.Lookup(name)
		if flag != nil && flag.Changed {
			set = append(set, "--"+name)
		}
	}
	if len(set) > 1 {
		sort.Strings(set)
		return fmt.Errorf("%s are mutually exclusive", strings.Join(set, ", "))
	}
	return nil
}
