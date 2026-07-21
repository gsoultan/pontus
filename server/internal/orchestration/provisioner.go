package orchestration

import (
	"context"
	"time"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Provisioner defines the interface for database provisioning operations.
type Provisioner interface {
	// ProvisionReplica orchestrates the setup of a new database replica.
	ProvisionReplica(ctx context.Context, req *endpoints.ProvisionReplicaRequest, progress chan<- *endpoints.ProvisionProgress) error

	// PromoteToPrimary promotes a replica to primary.
	PromoteToPrimary(ctx context.Context, backendAddr string) error

	// CheckReplicationLag returns the replication lag for a backend.
	CheckReplicationLag(ctx context.Context, backendAddr string) (time.Duration, error)

	// DemoteToReplica reconfigures a primary as a replica.
	DemoteToReplica(ctx context.Context, backendAddr string, primaryAddr string) error
}
