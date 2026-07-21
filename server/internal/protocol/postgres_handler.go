package protocol

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/pkg/buffer"
	"github.com/gsoultan/pontus/pkg/config"
)

var (
	builderPool = sync.Pool{
		New: func() any {
			return new(strings.Builder)
		},
	}
)

// PostgresHandler implements the protocol.Handler interface for PostgreSQL.
type PostgresHandler struct{}

// NewPostgresHandler creates a new PostgresHandler.
func NewPostgresHandler() *PostgresHandler {
	return &PostgresHandler{}
}

// Handshake manages the PostgreSQL startup sequence.
func (p *PostgresHandler) Handshake(ctx context.Context, client, server net.Conn, state *SessionState) error {
	buf := buffer.Get()
	defer buffer.Put(buf)

	// 1. Read StartupMessage from client
	n, err := client.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read startup message: %w", err)
	}

	// Extract user from startup message
	if n >= 8 {
		// PostgreSQL startup message: length(4), version(4), [key\0, value\0]*
		payload := buf[8:n]
		for {
			idx := bytes.IndexByte(payload, 0)
			if idx == -1 {
				break
			}
			key := string(payload[:idx])
			payload = payload[idx+1:]
			idx = bytes.IndexByte(payload, 0)
			if idx == -1 {
				break
			}
			value := string(payload[:idx])
			payload = payload[idx+1:]
			if key == "user" {
				state.User = value
				break
			}
		}
	}

	// 2. Forward to server
	if _, err := server.Write(buf[:n]); err != nil {
		return fmt.Errorf("failed to forward startup message: %w", err)
	}

	// 3. Forward server response until ReadyForQuery ('Z')
	for {
		n, err := server.Read(buf)
		if err != nil {
			return fmt.Errorf("failed to read from server during handshake: %w", err)
		}

		if _, err := client.Write(buf[:n]); err != nil {
			return fmt.Errorf("failed to forward server response: %w", err)
		}

		// Check if we got ReadyForQuery
		// Multiple messages might be in one packet
		for i := 0; i < n; {
			msgType := buf[i]
			if msgType == 'Z' {
				return nil
			}
			// Skip msgType (1) + length (4)
			if i+5 > n {
				break
			}
			length := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			i += 1 + length
		}
	}
}

// PeekTransactionState inspects PostgreSQL packets to determine the transaction status.
func (p *PostgresHandler) PeekTransactionState(data []byte) (TransactionState, error) {
	state := StatePartial

	for i := 0; i < len(data); {
		if i+5 > len(data) {
			break
		}

		msgType := data[i]
		length := int(uint32(data[i+1])<<24 | uint32(data[i+2])<<16 | uint32(data[i+3])<<8 | uint32(data[i+4]))

		if msgType == 'Z' && i+6 <= len(data) {
			status := data[i+5]
			switch status {
			case 'I':
				state = StateIdle
			case 'T', 'B':
				state = StateInTransaction
			case 'E':
				state = StateError
			}
		}

		// Move to next message
		next := i + 1 + length
		if next <= i { // Overflow or zero length loop protection
			break
		}
		i = next
	}

	return state, nil
}

// ClassifyQuery identifies if the given PostgreSQL query is read-only and extracts affected tables.
func (p *PostgresHandler) ClassifyQuery(data []byte) QueryInfo {
	query := p.extractQuery(data)
	if query == "" {
		return QueryInfo{ReadOnly: false, InTransaction: false}
	}

	info := QueryInfo{ReadOnly: false}
	tokens := Tokenize(query)

	var lastKeyword string
	for token := range tokens {
		if token.Type == TokenKeyword {
			switch token.Value {
			case "SELECT", "SHOW", "DESCRIBE", "EXPLAIN":
				info.ReadOnly = true
			case "BEGIN", "START":
				info.InTransaction = true
			case "FOR":
				// Check for FOR UPDATE/SHARE
				// We need to peek next tokens, but with iterator we can just set a flag
				lastKeyword = "FOR"
			case "UPDATE", "SHARE":
				if lastKeyword == "FOR" {
					info.ReadOnly = false
				}
			case "INSERT", "DELETE", "TRUNCATE", "DROP", "ALTER", "GRANT", "REVOKE":
				info.ReadOnly = false
			}

			if token.Value != "FOR" {
				lastKeyword = token.Value
			}
		} else if token.Type == TokenIdentifier {
			if lastKeyword == "FROM" || lastKeyword == "JOIN" || lastKeyword == "UPDATE" || lastKeyword == "INTO" || lastKeyword == "TABLE" {
				t := strings.ToLower(strings.Trim(token.Value, `"`))
				info.AffectedTables = append(info.AffectedTables, t)
			}
		}
	}

	// De-duplicate tables
	if len(info.AffectedTables) > 1 {
		seen := make(map[string]struct{})
		unique := info.AffectedTables[:0]
		for _, t := range info.AffectedTables {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				unique = append(unique, t)
			}
		}
		info.AffectedTables = unique
	}

	return info
}

func (p *PostgresHandler) hasPrefixFold(s, prefix []byte) bool {
	if len(s) < len(prefix) {
		return false
	}
	return bytes.EqualFold(s[:len(prefix)], prefix)
}

// NormalizeQuery strips values from the query.
func (p *PostgresHandler) NormalizeQuery(data []byte) string {
	query := p.extractQuery(data)
	if query == "" {
		return ""
	}
	// Pure Go fallback for normalization since CGO is disabled
	return p.basicNormalize(query)
}

func (p *PostgresHandler) basicNormalize(q string) string {
	// Simple heuristic normalization:
	// 1. Replace numbers with ?
	// 2. Replace strings with ?
	// 3. Replace IN (...) with IN (...)
	sb := builderPool.Get().(*strings.Builder)
	sb.Reset()
	defer builderPool.Put(sb)

	inString := false
	inEscape := false
	for _, r := range q {
		if inString {
			if inEscape {
				inEscape = false
				continue
			}
			if r == '\\' {
				inEscape = true
				continue
			}
			if r == '\'' {
				inString = false
				sb.WriteRune('?')
			}
			continue
		}
		if r == '\'' {
			inString = true
			continue
		}
		if r >= '0' && r <= '9' {
			if sb.Len() > 0 && sb.String()[sb.Len()-1] == '?' {
				continue
			}
			sb.WriteRune('?')
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func (p *PostgresHandler) extractQuery(data []byte) string {
	return string(p.extractQueryBytes(data))
}

func (p *PostgresHandler) extractQueryBytes(data []byte) []byte {
	if len(data) < 6 {
		return nil
	}

	msgType := data[0]
	switch msgType {
	case 'Q': // Simple query
		return data[5:]
	case 'P': // Parse
		// Skip msgType (1) + length (4)
		// Next is statement name (null-terminated string)
		nullIdx := bytes.IndexByte(data[5:], 0)
		if nullIdx != -1 {
			return data[5+nullIdx+1:]
		}
	}
	return nil
}

// TrackSessionState intercepts SET commands.
func (p *PostgresHandler) TrackSessionState(state *SessionState, data []byte) {
	if len(data) < 6 || data[0] != 'Q' {
		return
	}

	query := p.extractQueryBytes(data)
	if len(query) == 0 {
		return
	}

	// 1. Detect Session Pinning (LISTEN, LOCK, TEMPORARY)
	if p.hasPrefixFold(query, []byte("LISTEN ")) ||
		p.hasPrefixFold(query, []byte("LOCK ")) ||
		bytes.Contains(bytes.ToLower(query), []byte("temporary ")) ||
		bytes.Contains(bytes.ToLower(query), []byte("temp ")) {
		state.Pinned = true
	}

	// 2. Track SET commands
	if p.hasPrefixFold(query, []byte("SET ")) {
		if state.Vars == nil {
			state.Vars = make(map[string]string)
		}
		// Simplified parsing of SET key = value using bytes to avoid allocations
		trimmed := bytes.TrimSpace(query[4:])
		parts := bytes.Fields(trimmed)
		if len(parts) >= 2 {
			key := strings.ToLower(string(parts[0]))
			// If it's a multi-part value, join them
			var val string
			if len(parts) == 2 {
				val = string(parts[1])
			} else {
				val = string(bytes.Join(parts[1:], []byte(" ")))
			}
			state.Vars[key] = val
		}
	}
}

// ReplaySessionState replays tracked SET commands to a new connection.
func (p *PostgresHandler) ReplaySessionState(ctx context.Context, conn net.Conn, state *SessionState) error {
	if state == nil || len(state.Vars) == 0 {
		return nil
	}

	for k, v := range state.Vars {
		query := fmt.Sprintf("SET %s %s", k, v)
		payload := make([]byte, 1+4+len(query)+1)
		payload[0] = 'Q'
		length := 4 + len(query) + 1
		payload[1] = byte(length >> 24)
		payload[2] = byte(length >> 16)
		payload[3] = byte(length >> 8)
		payload[4] = byte(length)
		copy(payload[5:], query)
		payload[len(payload)-1] = 0

		if _, err := conn.Write(payload); err != nil {
			return err
		}

		// Read response until ReadyForQuery
		buf := buffer.Get()
		defer buffer.Put(buf)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return err
			}
			for i := 0; i < n; {
				msgType := buf[i]
				if msgType == 'Z' {
					goto nextVar
				}
				if i+5 > n {
					break
				}
				msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
				i += 1 + msgLen
			}
		}
	nextVar:
	}
	return nil
}

// TrackPreparedStatement tracks prepared statements in the session.
func (p *PostgresHandler) TrackPreparedStatement(state *SessionState, data []byte) {
	if len(data) < 6 || data[0] != 'P' {
		return
	}

	if state.Stmts == nil {
		state.Stmts = make(map[string]string)
	}

	// Message format: 'P' + length(4) + name(null-terminated) + query(null-terminated)
	nameEnd := bytes.IndexByte(data[5:], 0)
	if nameEnd == -1 {
		return
	}
	name := string(data[5 : 5+nameEnd])
	queryStart := 5 + nameEnd + 1
	queryEnd := bytes.IndexByte(data[queryStart:], 0)
	if queryEnd == -1 {
		return
	}
	query := string(data[queryStart : queryStart+queryEnd])
	state.Stmts[name] = query
}

// ReplayPreparedStatements replays tracked prepared statements.
func (p *PostgresHandler) ReplayPreparedStatements(ctx context.Context, conn net.Conn, state *SessionState) error {
	if state == nil || len(state.Stmts) == 0 {
		return nil
	}

	for name, query := range state.Stmts {
		// Construct 'P' message
		payload := make([]byte, 1+4+len(name)+1+len(query)+1+2)
		payload[0] = 'P'
		length := 4 + len(name) + 1 + len(query) + 1 + 2
		payload[1] = byte(length >> 24)
		payload[2] = byte(length >> 16)
		payload[3] = byte(length >> 8)
		payload[4] = byte(length)
		copy(payload[5:], name)
		payload[5+len(name)] = 0
		copy(payload[5+len(name)+1:], query)
		payload[5+len(name)+1+len(query)] = 0
		// Number of parameter data types (0)
		payload[len(payload)-2] = 0
		payload[len(payload)-1] = 0

		if _, err := conn.Write(payload); err != nil {
			return err
		}

		// Also send Sync 'S' to ensure it's processed
		syncMsg := []byte{'S', 0, 0, 0, 4}
		if _, err := conn.Write(syncMsg); err != nil {
			return err
		}

		// Read response until ReadyForQuery
		buf := buffer.Get()
		defer buffer.Put(buf)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return err
			}
			for i := 0; i < n; {
				msgType := buf[i]
				if msgType == 'Z' {
					goto nextStmt
				}
				if i+5 > n {
					break
				}
				msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
				i += 1 + msgLen
			}
		}
	nextStmt:
	}
	return nil
}

// Identify returns PostgreSQL metadata.
func (p *PostgresHandler) Identify() Metadata {
	return Metadata{
		Name:    "PostgreSQL",
		Port:    5432,
		Version: "18.0", // Default version, can be updated as new versions are released
	}
}

// IsPinned returns true if the session must not be unpooled.
func (p *PostgresHandler) IsPinned(state *SessionState) bool {
	if state == nil {
		return false
	}
	return state.Pinned
}

// DeepCheck executes a simple SELECT 1 to verify database liveness.
func (p *PostgresHandler) DeepCheck(ctx context.Context, conn net.Conn) error {
	return p.Execute(ctx, conn, "SELECT 1")
}

// Execute sends a simple query and waits for ReadyForQuery.
func (p *PostgresHandler) Execute(ctx context.Context, conn net.Conn, query string) error {
	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return err
	}

	// Read response until ReadyForQuery
	buf := buffer.Get()
	defer buffer.Put(buf)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return err
		}
		// Check for ErrorResponse 'E'
		for i := 0; i < n; {
			msgType := buf[i]
			if msgType == 'E' {
				return fmt.Errorf("postgres error: %s", string(buf[i:n]))
			}
			if msgType == 'Z' {
				return nil
			}
			if i+5 > n {
				break
			}
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			i += 1 + msgLen
		}
	}
}

// IsReadOnly checks if the Postgres node is in recovery (replica).
func (p *PostgresHandler) IsReadOnly(ctx context.Context, conn net.Conn) (bool, error) {
	query := "SELECT pg_is_in_recovery()"
	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return false, err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	isRecovery := false
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return false, err
		}
		for i := 0; i < n; {
			msgType := buf[i]
			if msgType == 'D' { // DataRow
				// For SELECT pg_is_in_recovery(), we expect one row with one column
				// Skip type(1) + len(4) + numCols(2) + colLen(4)
				if i+11 < n {
					val := buf[i+11]
					if val == 't' {
						isRecovery = true
					}
				}
			}
			if msgType == 'Z' {
				return isRecovery, nil
			}
			if i+5 > n {
				break
			}
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			i += 1 + msgLen
		}
	}
}

// IsReadOnlyError checks for Postgres error code 25006 (read_only_sql_transaction).
func (p *PostgresHandler) IsReadOnlyError(data []byte) bool {
	if len(data) < 5 || data[0] != 'E' {
		return false
	}

	// Simple check for "25006" in the error response payload.
	// A proper implementation would parse the ErrorResponse fields.
	return bytes.Contains(data, []byte("25006"))
}

// GetReplicationLag returns the replication lag in seconds.
func (p *PostgresHandler) GetReplicationLag(ctx context.Context, conn net.Conn) (time.Duration, error) {
	// Query for lag. If primary, returns 0.
	query := "SELECT CASE WHEN pg_is_in_recovery() THEN COALESCE(EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp())), 0) ELSE 0 END"
	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return 0, err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	var lag float64
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return 0, err
		}
		for i := 0; i < n; {
			msgType := buf[i]
			if msgType == 'D' { // DataRow
				// Skip type(1) + len(4) + numCols(2) + colLen(4)
				if i+11 < n {
					colLen := int(uint32(buf[i+7])<<24 | uint32(buf[i+8])<<16 | uint32(buf[i+9])<<8 | uint32(buf[i+10]))
					if colLen > 0 && i+11+colLen <= n {
						valStr := string(buf[i+11 : i+11+colLen])
						fmt.Sscanf(valStr, "%f", &lag)
					}
				}
			}
			if msgType == 'Z' {
				return time.Duration(lag * float64(time.Second)), nil
			}
			if i+5 > n {
				break
			}
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			i += 1 + msgLen
		}
	}
}

// CreateReplicationSlot creates a physical replication slot.
func (p *PostgresHandler) CreateReplicationSlot(ctx context.Context, conn net.Conn, slotName string) error {
	query := fmt.Sprintf("SELECT pg_create_physical_replication_slot('%s')", slotName)
	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return err
		}
		for i := 0; i < n; {
			msgType := buf[i]
			if msgType == 'E' { // ErrorResponse
				return fmt.Errorf("postgres error: %s", string(buf[i:n]))
			}
			if msgType == 'Z' {
				return nil
			}
			if i+5 > n {
				break
			}
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			i += 1 + msgLen
		}
	}
}

// DiscoverTopology returns a list of replica addresses from pg_stat_replication.
func (p *PostgresHandler) DiscoverTopology(ctx context.Context, conn net.Conn) ([]string, error) {
	query := "SELECT client_addr FROM pg_stat_replication"
	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	var replicas []string
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		for i := 0; i < n; {
			msgType := buf[i]
			if msgType == 'D' { // DataRow
				if i+11 < n {
					colLen := int(uint32(buf[i+7])<<24 | uint32(buf[i+8])<<16 | uint32(buf[i+9])<<8 | uint32(buf[i+10]))
					if colLen > 0 && i+11+colLen <= n {
						addr := string(buf[i+11 : i+11+colLen])
						if addr != "" {
							replicas = append(replicas, addr)
						}
					}
				}
			}
			if msgType == 'Z' {
				return replicas, nil
			}
			if msgType == 'E' {
				return nil, fmt.Errorf("postgres error: %s", string(buf[i:n]))
			}
			if i+5 > n {
				break
			}
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			i += 1 + msgLen
		}
	}
}

// GetCurrentLSN returns the current WAL LSN from the primary.
func (p *PostgresHandler) GetCurrentLSN(ctx context.Context, conn net.Conn) (string, error) {
	query := "SELECT pg_current_wal_lsn()"
	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return "", err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	var lsn string
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return "", err
		}
		for i := 0; i < n; {
			msgType := buf[i]
			if msgType == 'D' {
				if i+11 < n {
					colLen := int(uint32(buf[i+7])<<24 | uint32(buf[i+8])<<16 | uint32(buf[i+9])<<8 | uint32(buf[i+10]))
					if colLen > 0 && i+11+colLen <= n {
						lsn = string(buf[i+11 : i+11+colLen])
					}
				}
			}
			if msgType == 'Z' {
				return lsn, nil
			}
			if i+5 > n {
				break
			}
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			i += 1 + msgLen
		}
	}
}

// WaitLSN waits until the replica's replayed LSN is greater than or equal to the target LSN.
func (p *PostgresHandler) WaitLSN(ctx context.Context, conn net.Conn, targetLSN string) error {
	if targetLSN == "" {
		return nil
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		query := fmt.Sprintf("SELECT pg_last_wal_replay_lsn() >= '%s'::pg_lsn", targetLSN)
		payload := make([]byte, 1+4+len(query)+1)
		payload[0] = 'Q'
		length := 4 + len(query) + 1
		payload[1] = byte(length >> 24)
		payload[2] = byte(length >> 16)
		payload[3] = byte(length >> 8)
		payload[4] = byte(length)
		copy(payload[5:], query)
		payload[len(payload)-1] = 0

		if _, err := conn.Write(payload); err != nil {
			return err
		}

		buf := buffer.Get()
		caughtUp := false
		for {
			n, err := conn.Read(buf)
			if err != nil {
				buffer.Put(buf)
				return err
			}
			for i := 0; i < n; {
				msgType := buf[i]
				if msgType == 'D' {
					if i+11 < n && buf[i+11] == 't' {
						caughtUp = true
					}
				}
				if msgType == 'Z' {
					goto checkCaughtUp
				}
				if i+5 > n {
					break
				}
				msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
				i += 1 + msgLen
			}
		}

	checkCaughtUp:
		buffer.Put(buf)
		if caughtUp {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			continue
		}
	}
}

// CollectMetrics gathers detailed metrics from the PostgreSQL database.
func (p *PostgresHandler) CollectMetrics(ctx context.Context, conn net.Conn) (*domain.DatabaseMetrics, error) {
	metrics := &domain.DatabaseMetrics{}

	// 1. Check recovery status
	isRecovery, err := p.IsReadOnly(ctx, conn)
	if err == nil {
		metrics.IsRecovery = isRecovery
	}

	// Since we need to capture data, let's use a specialized query execution for metrics.
	query := `
		SELECT 
			(SELECT count(*) FROM pg_stat_activity),
			(SELECT setting::int FROM pg_settings WHERE name = 'max_connections'),
			sum(xact_commit), 
			sum(xact_rollback), 
			sum(blks_read), 
			sum(blks_hit), 
			sum(conflicts), 
			sum(deadlocks)
		FROM pg_stat_database
	`

	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		for i := 0; i < n; {
			if i+5 > n {
				break
			}
			msgType := buf[i]
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))

			if msgType == 'D' { // DataRow
				// We expect 8 columns
				// type(1) + len(4) + numCols(2)
				pos := i + 7
				for col := 0; col < 8; col++ {
					if pos+4 > i+1+msgLen || pos+4 > n {
						break
					}
					colLen := int(int32(uint32(buf[pos])<<24 | uint32(buf[pos+1])<<16 | uint32(buf[pos+2])<<8 | uint32(buf[pos+3])))
					pos += 4
					if colLen > 0 && pos+colLen <= i+1+msgLen && pos+colLen <= n {
						valStr := string(buf[pos : pos+colLen])
						var val int64
						fmt.Sscanf(valStr, "%d", &val)
						switch col {
						case 0:
							metrics.ActiveBackends = val
						case 1:
							metrics.MaxBackends = val
						case 2:
							metrics.TransactionsCommitted = val
						case 3:
							metrics.TransactionsRolledBack = val
						case 4:
							metrics.BlocksRead = val
						case 5:
							metrics.BlocksHit = val
						case 6:
							metrics.Conflicts = val
						case 7:
							metrics.Deadlocks = val
						}
						pos += colLen
					} else if colLen == -1 {
						// Null value, do nothing
					}
				}
			}
			if msgType == 'Z' {
				goto replicationLag
			}
			if msgType == 'E' {
				return nil, fmt.Errorf("postgres error during metrics collection: %s", string(buf[i:min(n, i+1+msgLen)]))
			}

			i += 1 + msgLen
		}
	}

replicationLag:
	if metrics.BlocksHit+metrics.BlocksRead > 0 {
		metrics.CacheHitRatio = float32(metrics.BlocksHit) / float32(metrics.BlocksHit+metrics.BlocksRead)
	}

	if metrics.IsRecovery {
		// Get replication lag in bytes
		lagQuery := "SELECT pg_wal_lsn_diff(pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn())"
		if err := p.queryInt64(ctx, conn, lagQuery, &metrics.ReplicationLagBytes); err != nil {
			// Ignore error, might not be a replica or function not available
		}
	}

	return metrics, nil
}

func (p *PostgresHandler) queryInt64(ctx context.Context, conn net.Conn, query string, target *int64) error {
	payload := make([]byte, 1+4+len(query)+1)
	payload[0] = 'Q'
	length := 4 + len(query) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], query)
	payload[len(payload)-1] = 0

	if _, err := conn.Write(payload); err != nil {
		return err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		for i := 0; i < n; {
			if i+5 > n {
				break
			}
			msgType := buf[i]
			msgLen := int(uint32(buf[i+1])<<24 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<8 | uint32(buf[i+4]))
			if msgType == 'D' {
				if i+11 < n {
					colLen := int(uint32(buf[i+7])<<24 | uint32(buf[i+8])<<16 | uint32(buf[i+9])<<8 | uint32(buf[i+10]))
					if colLen > 0 && i+11+colLen <= n {
						valStr := string(buf[i+11 : i+11+colLen])
						fmt.Sscanf(valStr, "%d", target)
					}
				}
			}
			if msgType == 'Z' {
				return nil
			}
			if msgType == 'E' {
				return fmt.Errorf("postgres error: %s", string(buf[i:min(n, i+1+msgLen)]))
			}
			i += 1 + msgLen
		}
	}
}

func (p *PostgresHandler) RewriteQuery(data []byte, rules []config.MaskingRule) ([]byte, error) {
	if len(data) < 6 || data[0] != 'Q' {
		return data, nil
	}

	query := p.extractQuery(data)
	if query == "" {
		return data, nil
	}

	modified := false
	tokens := slices.Collect(Tokenize(query))
	var sb strings.Builder

	for i, t := range tokens {
		if i > 0 {
			sb.WriteRune(' ')
		}

		val := t.Value
		if t.Type == TokenIdentifier {
			for _, rule := range rules {
				if strings.EqualFold(t.Value, rule.Column) {
					switch rule.Format {
					case "hash":
						val = fmt.Sprintf("md5(%s)", t.Value)
						modified = true
					case "redact":
						val = "'REDACTED'"
						modified = true
					case "mask":
						val = "regexp_replace(" + t.Value + ", '.', '*', 'g')"
						modified = true
					}
					break
				}
			}
		}
		sb.WriteString(val)
	}

	if !modified {
		return data, nil
	}

	newQuery := sb.String()
	// Re-encode 'Q' message
	payload := make([]byte, 1+4+len(newQuery)+1)
	payload[0] = 'Q'
	length := 4 + len(newQuery) + 1
	payload[1] = byte(length >> 24)
	payload[2] = byte(length >> 16)
	payload[3] = byte(length >> 8)
	payload[4] = byte(length)
	copy(payload[5:], newQuery)
	payload[len(payload)-1] = 0
	return payload, nil
}
