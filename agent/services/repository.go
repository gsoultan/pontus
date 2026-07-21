package services

import "context"

// RepositoryManager handles interaction with external package repositories.
type RepositoryManager interface {
	// GetPostgresVersions returns a map of major version to latest available version from the repository.
	GetPostgresVersions(ctx context.Context) (map[int]string, error)
	// IsOSVersionOutdated checks if the OS default version is lower than what's available in the official repository.
	IsOSVersionOutdated(ctx context.Context, major int) (bool, error)
	// AddPostgresRepository adds the official PostgreSQL repository to the system.
	AddPostgresRepository(ctx context.Context) error
}
