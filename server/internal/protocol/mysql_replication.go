package protocol

import (
	"context"
	"errors"
	"net"
)

// StartReplication reports that MySQL replication is not proxied.
//
// The binlog protocol is a different exchange from PostgreSQL's CopyBoth
// stream, and nothing here implements it. Failing explicitly keeps a MySQL
// client from being handed a pooled session that would be recycled underneath
// its stream.
func (m *MySQLHandler) StartReplication(_ context.Context, _, _ net.Conn, _ *SessionState) error {
	return errors.New("mysql replication streams are not proxied")
}
