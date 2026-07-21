package validator

import (
	"context"
	"fmt"
	"strings"
)

// Postgres implements the Validator interface for PostgreSQL.
type Postgres struct{}

// Validate checks if the pg_hba.conf content is valid.
func (v *Postgres) Validate(ctx context.Context, filePath string, content string) error {
	if strings.Contains(filePath, "pg_hba.conf") {
		if !strings.Contains(content, "host") && !strings.Contains(content, "local") {
			return fmt.Errorf("invalid pg_hba.conf: must contain host or local entries")
		}
	}
	return nil
}
