// Package migrations embeds the SQL schema so a single binary is self-contained.
package migrations

import (
	"embed"
	"fmt"
)

//go:embed *.sql
var files embed.FS

// SQL returns the ordered migration statements.
func SQL() ([]string, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	statements := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := files.ReadFile(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		statements = append(statements, string(content))
	}
	return statements, nil
}
