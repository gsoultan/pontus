package protocol

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/pkg/buffer"
	"github.com/pingcap/tidb/pkg/parser"
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

// MySQLHandler implements the protocol.Handler interface for MySQL.
type MySQLHandler struct{}

// NewMySQLHandler creates a new MySQLHandler.
func NewMySQLHandler() *MySQLHandler {
	return &MySQLHandler{}
}

// Handshake manages the MySQL handshake.
func (m *MySQLHandler) Handshake(ctx context.Context, client, server net.Conn, state *SessionState) error {
	buf := buffer.Get()
	defer buffer.Put(buf)

	// 1. Read Initial Handshake from server
	n, err := server.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read server handshake: %w", err)
	}

	// 2. Forward to client
	if _, err := client.Write(buf[:n]); err != nil {
		return fmt.Errorf("failed to forward server handshake: %w", err)
	}

	// 3. Read Handshake Response from client
	n, err = client.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read client response: %w", err)
	}

	// 4. Forward to server
	if _, err := server.Write(buf[:n]); err != nil {
		return fmt.Errorf("failed to forward client response: %w", err)
	}

	// 5. Read Auth Result from server
	n, err = server.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read auth result: %w", err)
	}

	// 6. Forward to client
	if _, err := client.Write(buf[:n]); err != nil {
		return fmt.Errorf("failed to forward auth result: %w", err)
	}

	return nil
}

// PeekTransactionState inspects MySQL packets.
func (m *MySQLHandler) PeekTransactionState(data []byte) (TransactionState, error) {
	state := StatePartial

	for i := 0; i < len(data); {
		if i+4 > len(data) {
			break
		}

		// MySQL packet header: 3 bytes length, 1 byte sequence id
		payloadLength := int(uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16)
		if i+4+payloadLength > len(data) {
			break
		}

		payload := data[i+4 : i+4+payloadLength]
		if len(payload) > 0 {
			msgType := payload[0]
			// 0x00: OK_Packet, 0xfe: EOF_Packet
			if msgType == 0x00 || msgType == 0xfe {
				// Status flags indicate if we are in a transaction.
				// In OK_Packet (Protocol 41), it's after affected_rows and last_insert_id.
				// For simplicity, we check if the IN_TRANS flag is set.
				// SERVER_STATUS_IN_TRANS = 0x0001

				var statusFlags uint16
				if msgType == 0x00 && len(payload) >= 7 {
					// Heuristic: status flags are often in these positions for simple OK packets.
					statusFlags = uint16(payload[len(payload)-4]) | uint16(payload[len(payload)-3])<<8
				} else if msgType == 0xfe && len(payload) >= 5 {
					statusFlags = uint16(payload[3]) | uint16(payload[4])<<8
				}

				if statusFlags&0x0001 == 0 {
					state = StateIdle
				} else {
					state = StateInTransaction
				}
			}
		}

		// Move to next packet
		next := i + 4 + payloadLength
		if next <= i { // Overflow protection
			break
		}
		i = next
	}

	return state, nil
}

// ClassifyQuery identifies if the given MySQL query is read-only and extracts affected tables.
func (m *MySQLHandler) ClassifyQuery(data []byte) QueryInfo {
	query := m.extractQuery(data)
	if query == "" {
		return QueryInfo{ReadOnly: false}
	}

	upper := strings.ToUpper(strings.TrimSpace(query))
	tables := m.extractTables(query)

	if strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "EXPLAIN") {

		if strings.HasPrefix(upper, "SELECT") &&
			(strings.Contains(upper, "FOR UPDATE") || strings.Contains(upper, "LOCK IN SHARE MODE") || strings.Contains(upper, "FOR SHARE")) {
			return QueryInfo{ReadOnly: false, AffectedTables: tables}
		}

		return QueryInfo{ReadOnly: true, AffectedTables: tables}
	}

	return QueryInfo{ReadOnly: false, AffectedTables: tables}
}

var mysqlTableRegex = regexp.MustCompile(`(?i)(?:FROM|JOIN|UPDATE|INTO|TABLE)\s+([a-z0-9_".]+)`)

func (m *MySQLHandler) extractTables(query string) []string {
	matches := mysqlTableRegex.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		return nil
	}
	tables := make([]string, 0, len(matches))
	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(match) > 1 {
			t := strings.ToLower(strings.Trim(match[1], "`\""))
			if _, ok := seen[t]; !ok {
				tables = append(tables, t)
				seen[t] = struct{}{}
			}
		}
	}
	return tables
}

// NormalizeQuery strips values from the query.
func (m *MySQLHandler) NormalizeQuery(data []byte) string {
	query := m.extractQuery(data)
	if query == "" {
		return ""
	}
	return parser.DigestNormalized(query).String()
}

func (m *MySQLHandler) extractQuery(data []byte) string {
	if len(data) < 5 {
		return ""
	}
	payloadLength := int(uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16)
	if len(data) < 4+payloadLength {
		return ""
	}
	payload := data[4 : 4+payloadLength]
	if len(payload) == 0 {
		return ""
	}

	msgType := payload[0]
	if msgType == 0x03 { // COM_QUERY
		return string(payload[1:])
	}
	if msgType == 0x16 { // COM_STMT_PREPARE
		return string(payload[1:])
	}
	return ""
}

// IsTerminate reports COM_QUIT.
func (m *MySQLHandler) IsTerminate(data []byte) bool {
	const comQuit = 0x01
	return len(data) >= 5 && data[4] == comQuit
}

// Cacheable allows only COM_QUERY, the text protocol's whole-query command.
//
// COM_STMT_PREPARE and COM_STMT_EXECUTE are steps in a prepared-statement
// exchange whose replies are not result sets, for the same reason PostgreSQL's
// extended protocol is excluded: a cached result written in place of a prepare
// acknowledgement leaves both sides disagreeing about where they are.
func (m *MySQLHandler) Cacheable(data []byte) bool {
	const comQuery = 0x03
	// 3-byte payload length, 1-byte sequence id, then the command byte.
	if len(data) < 5 {
		return false
	}
	return data[4] == comQuery
}

// TrackSessionState intercepts SET commands.
func (m *MySQLHandler) TrackSessionState(state *SessionState, data []byte) {
	if len(data) < 5 {
		return
	}
	payloadLength := int(uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16)
	if len(data) < 4+payloadLength {
		return
	}
	payload := data[4 : 4+payloadLength]
	if len(payload) == 0 || payload[0] != 0x03 { // COM_QUERY
		return
	}

	query := strings.TrimSpace(string(payload[1:]))
	upper := strings.ToUpper(query)

	if strings.HasPrefix(upper, "SET ") {
		if state.Vars == nil {
			state.Vars = make(map[string]string)
		}
		parts := strings.Fields(query[4:])
		if len(parts) >= 2 {
			key := strings.ToLower(parts[0])
			val := strings.Join(parts[1:], " ")
			state.Vars[key] = val
		}
	}
}

// ReplaySessionState replays tracked SET commands to a new connection.
func (m *MySQLHandler) ReplaySessionState(ctx context.Context, conn net.Conn, state *SessionState) error {
	if state == nil || len(state.Vars) == 0 {
		return nil
	}

	for k, v := range state.Vars {
		query := fmt.Sprintf("SET %s %s", k, v)
		if err := m.writeQuery(conn, query); err != nil {
			return err
		}

		buf := buffer.Get()
		defer buffer.Put(buf)
		if _, err := conn.Read(buf); err != nil {
			return err
		}
	}
	return nil
}

// TrackPreparedStatement tracks prepared statements.
func (m *MySQLHandler) TrackPreparedStatement(state *SessionState, data []byte) {
	if len(data) < 5 {
		return
	}
	payloadLength := int(uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16)
	if len(data) < 4+payloadLength {
		return
	}
	payload := data[4 : 4+payloadLength]
	if len(payload) == 0 || payload[0] != 0x16 { // COM_STMT_PREPARE
		return
	}

	if state.Stmts == nil {
		state.Stmts = make(map[string]string)
	}

	query := string(payload[1:])
	// For MySQL, prepared statements are often identified by ID.
	// However, COM_STMT_PREPARE doesn't include a name like Postgres.
	// The server returns a statement ID.
	// To truly support this across pooled connections, we'd need to intercept the response
	// and map IDs.
	// For now, we store the query to allow re-preparing on a different backend if needed,
	// though this requires more complex logic to handle COM_STMT_EXECUTE.
	state.Stmts[query] = query
}

// ReplayPreparedStatements is a placeholder for MySQL.
func (m *MySQLHandler) ReplayPreparedStatements(ctx context.Context, conn net.Conn, state *SessionState) error {
	// MySQL replaying is complex because it's ID-based and requires response interception.
	return nil
}

// CollectMetrics gathers metrics for MySQL.
func (m *MySQLHandler) CollectMetrics(ctx context.Context, conn net.Conn) (*domain.DatabaseMetrics, error) {
	// Not implemented for MySQL yet.
	return &domain.DatabaseMetrics{}, nil
}

func (m *MySQLHandler) writeQuery(conn net.Conn, query string) error {
	payload := make([]byte, 4+1+len(query))
	payloadLength := 1 + len(query)
	payload[0] = byte(payloadLength)
	payload[1] = byte(payloadLength >> 8)
	payload[2] = byte(payloadLength >> 16)
	payload[3] = 0    // sequence id
	payload[4] = 0x03 // COM_QUERY
	copy(payload[5:], query)

	_, err := conn.Write(payload)
	return err
}

// Identify returns MySQL metadata.
func (m *MySQLHandler) Identify() Metadata {
	return Metadata{
		Name:    "MySQL",
		Port:    3306,
		Version: "9.2",
	}
}

// IsPinned returns true if the session must not be unpooled.
func (m *MySQLHandler) IsPinned(state *SessionState) bool {
	if state == nil {
		return false
	}
	return state.Pinned
}

// DeepCheck executes a MySQL ping.
func (m *MySQLHandler) DeepCheck(ctx context.Context, conn net.Conn) error {
	// COM_PING: payload 0x0e
	payload := []byte{1, 0, 0, 0, 0x0e}
	if _, err := conn.Write(payload); err != nil {
		return err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}

	if n >= 5 && buf[4] == 0xff { // ERR_Packet
		return fmt.Errorf("mysql error during ping")
	}
	return nil
}

// Execute sends a COM_QUERY and waits for a response.
func (m *MySQLHandler) Execute(ctx context.Context, conn net.Conn, query string) error {
	if err := m.writeQuery(conn, query); err != nil {
		return err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}

	if n >= 5 && buf[4] == 0xff { // ERR_Packet
		return fmt.Errorf("mysql error: %s", string(buf[4:n]))
	}
	return nil
}

// IsReadOnly checks the @@read_only global variable.
func (m *MySQLHandler) IsReadOnly(ctx context.Context, conn net.Conn) (bool, error) {
	query := "SELECT @@read_only"
	payload := make([]byte, 4+1+len(query))
	payloadLength := 1 + len(query)
	payload[0] = byte(payloadLength)
	payload[1] = byte(payloadLength >> 8)
	payload[2] = byte(payloadLength >> 16)
	payload[3] = 0    // sequence id
	payload[4] = 0x03 // COM_QUERY
	copy(payload[5:], query)

	if _, err := conn.Write(payload); err != nil {
		return false, err
	}

	buf := buffer.Get()
	defer buffer.Put(buf)
	n, err := conn.Read(buf)
	if err != nil {
		return false, err
	}

	// We expect a Resultset.
	// For simplicity, look for the value in the response.
	// @@read_only = 1 (true) or 0 (false).
	// The response will contain column definitions then data rows.
	// Heuristic: check if '1' or '0' is present in the payload.
	// This is not perfect but works for this specific query.
	if strings.Contains(string(buf[:n]), "1") {
		return true, nil
	}
	return false, nil
}

// GetReplicationLag returns the replication lag.
func (m *MySQLHandler) GetReplicationLag(ctx context.Context, conn net.Conn) (time.Duration, error) {
	// Dummy implementation
	return 0, nil
}

// IsReadOnlyError checks if the error is a read-only error.
func (m *MySQLHandler) IsReadOnlyError(data []byte) bool {
	// MySQL error 1290 (ER_OPTION_PREVENTS_STATEMENT)
	return strings.Contains(string(data), "1290")
}

// DiscoverTopology is not yet implemented for MySQL.
func (m *MySQLHandler) DiscoverTopology(ctx context.Context, conn net.Conn) ([]string, error) {
	return nil, nil
}

// GetCurrentLSN is a stub for MySQL.
func (m *MySQLHandler) GetCurrentLSN(ctx context.Context, conn net.Conn) (string, error) {
	return "", nil
}

// WaitLSN is a stub for MySQL.
func (m *MySQLHandler) WaitLSN(ctx context.Context, conn net.Conn, targetLSN string) error {
	return nil
}
