package registry

import (
	"log/slog"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/credentials"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
)

// buildCredentialStore assembles the lookup Pontus authenticates clients
// against, or returns nil to stay in passthrough mode.
//
// Returning nil is a normal outcome, not a failure: passthrough is the default
// and has to keep working with no auth block at all. What must not happen is
// silently running in passthrough when an operator asked for "pontus" — that
// would leave them believing clients are authenticated here when the exchange
// is still being relayed. Those cases log at error level and stay in
// passthrough, which is the safe direction: the old path still authenticates
// every client against the database.
func buildCredentialStore(cfg *config.Options, backends []pool2.Backend) credentials.Store {
	if cfg == nil || !cfg.PontusAuth() {
		return nil
	}
	opts := cfg.AuthOptions()

	var source credentials.Store
	switch {
	case opts.File != "":
		fileStore, err := credentials.NewFileStore(opts.File)
		if err != nil {
			slog.Error("auth.mode is \"pontus\" but the auth file is unusable; "+
				"staying in passthrough", "file", opts.File, "error", err)
			return nil
		}
		slog.Info("Authenticating clients against an auth file",
			"file", opts.File, "roles", fileStore.Len())
		source = fileStore

	default:
		querier := firstAdminSession(backends)
		if querier == nil {
			slog.Error("auth.mode is \"pontus\" but no backend has an admin_dsn to run " +
				"auth_query over; staying in passthrough")
			return nil
		}
		queryStore, err := credentials.NewQueryStore(querier, opts.Query)
		if err != nil {
			slog.Error("auth.mode is \"pontus\" but auth_query is unusable; "+
				"staying in passthrough", "error", err)
			return nil
		}
		slog.Info("Authenticating clients against auth_query")
		source = queryStore
	}

	return credentials.NewCache(source, opts.CacheTTL, opts.NegativeCacheTTL, opts.CacheSize)
}

// firstAdminSession finds a backend with a usable privileged connection.
//
// Any backend will do: roles are cluster-wide in PostgreSQL, so a verifier read
// from one node is the verifier on every node.
func firstAdminSession(backends []pool2.Backend) credentials.Querier {
	for _, backend := range backends {
		server, ok := backend.(*pool2.Server)
		if !ok {
			continue
		}
		if admin := server.AdminSession(); admin != nil {
			return credentials.SQLQuerier{DB: admin}
		}
	}
	return nil
}
