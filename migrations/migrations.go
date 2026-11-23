// Package migrations embeds the SQL schema so a single binary is self-contained.
package migrations

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Migration is one versioned SQL file.
type Migration struct {
	Version string
	Name    string
	SQL     string
}

// All returns every embedded migration ordered by filename. Filenames must
// begin with a zero-padded numeric version followed by an underscore.
func All() ([]Migration, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	migrations := make([]Migration, 0, len(names))
	for _, name := range names {
		version, _, ok := strings.Cut(name, "_")
		if !ok || version == "" {
			return nil, fmt.Errorf("migration %q must start with a version prefix", name)
		}
		content, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		migrations = append(migrations, Migration{Version: version, Name: name, SQL: string(content)})
	}
	return migrations, nil
}
