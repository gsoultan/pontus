package protocol

import (
	"io"
	"net"
	"testing"
)

type mockConn struct {
	net.Conn
	readPackets [][]byte
	packetIdx   int
	writeBuf    []byte
}

func (m *mockConn) Read(b []byte) (int, error) {
	if m.packetIdx >= len(m.readPackets) {
		return 0, io.EOF
	}
	pkt := m.readPackets[m.packetIdx]
	n := copy(b, pkt)
	if n < len(pkt) {
		m.readPackets[m.packetIdx] = pkt[n:]
	} else {
		m.packetIdx++
	}
	return n, nil
}

func (m *mockConn) Write(b []byte) (int, error) {
	m.writeBuf = append(m.writeBuf, b...)
	return len(b), nil
}

func TestPostgresHandler_Handshake(t *testing.T) {
	handler := NewPostgresHandler()

	// Startup message
	startupMsg := []byte{0, 0, 0, 8, 0, 3, 0, 0}
	client := &mockConn{readPackets: [][]byte{startupMsg}}

	// Server response: AuthenticationOk (R) + ReadyForQuery (Z)
	serverResp := []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0, 'Z', 0, 0, 0, 5, 'I'}
	server := &mockConn{readPackets: [][]byte{serverResp}}

	err := handler.Handshake(t.Context(), client, server, &SessionState{})
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	// Check if startup message reached server
	if string(server.writeBuf) != string(startupMsg) {
		t.Errorf("Expected server to receive startup message, got %v", server.writeBuf)
	}

	// Check if server response reached client
	if string(client.writeBuf) != string(serverResp) {
		t.Errorf("Expected client to receive server response, got %v", client.writeBuf)
	}
}

func TestPostgresHandler_CollectMetrics(t *testing.T) {
	handler := NewPostgresHandler()

	var packets [][]byte

	// 1. Response for IsReadOnly (pg_is_in_recovery) -> true
	p1 := []byte{'D', 0, 0, 0, 11, 0, 1} // DataRow, len 11, 1 col
	val := "t"
	p1 = append(p1, 0, 0, 0, byte(len(val)))
	p1 = append(p1, val...)
	p1 = append(p1, 'Z', 0, 0, 0, 5, 'I') // ReadyForQuery
	packets = append(packets, p1)

	// 2. Response for big query (8 columns)
	values := []string{"10", "100", "1000", "5", "500", "400", "0", "1"}
	p2 := []byte{'D', 0, 0, 0, 56, 0, 8}
	for _, v := range values {
		l := int32(len(v))
		p2 = append(p2, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
		p2 = append(p2, []byte(v)...)
	}
	p2 = append(p2, 'Z', 0, 0, 0, 5, 'I')
	packets = append(packets, p2)

	// 3. Response for replication lag
	p3 := []byte{'D', 0, 0, 0, 15, 0, 1} // DataRow, 1 col
	lag := "12345"
	p3 = append(p3, 0, 0, 0, byte(len(lag)))
	p3 = append(p3, lag...)
	p3 = append(p3, 'Z', 0, 0, 0, 5, 'I')
	packets = append(packets, p3)

	conn := &mockConn{readPackets: packets}
	metrics, err := handler.CollectMetrics(t.Context(), conn)
	if err != nil {
		t.Fatalf("CollectMetrics failed: %v", err)
	}

	if !metrics.IsRecovery {
		t.Error("Expected IsRecovery to be true")
	}
	if metrics.ActiveBackends != 10 {
		t.Errorf("Expected ActiveBackends 10, got %d", metrics.ActiveBackends)
	}
	if metrics.MaxBackends != 100 {
		t.Errorf("Expected MaxBackends 100, got %d", metrics.MaxBackends)
	}
	if metrics.TransactionsCommitted != 1000 {
		t.Errorf("Expected TransactionsCommitted 1000, got %d", metrics.TransactionsCommitted)
	}
	if metrics.ReplicationLagBytes != 12345 {
		t.Errorf("Expected ReplicationLagBytes 12345, got %d", metrics.ReplicationLagBytes)
	}
}
