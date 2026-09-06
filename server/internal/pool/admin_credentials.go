package pool

import "net/url"

// AdminCredentials returns the user and password from this backend's
// `admin_dsn`, or empty strings when none is configured.
//
// Rebuilding a node as a replica means running pg_basebackup against its new
// primary, which needs an account that can open a replication connection. The
// admin DSN is the only credential Pontus holds for a backend — it cannot
// derive one from a client, and guessing would be worse than not having one.
//
// The DSN itself is deliberately not exposed. A caller needs the two fields; a
// caller that had the whole string would eventually log it.
func (p *Server) AdminCredentials() (user, password string) {
	if p == nil || p.adminDSN == "" {
		return "", ""
	}

	parsed, err := url.Parse(p.adminDSN)
	if err != nil || parsed.User == nil {
		return "", ""
	}
	pass, _ := parsed.User.Password()
	return parsed.User.Username(), pass
}
