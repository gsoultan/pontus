package proxy

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"fmt"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// consoleBackend is a backend that reports pool occupancy. It embeds the
// package's existing double so it only has to say what the console reads.
type consoleBackend struct {
	*mockBackend
	addr     string
	pools    []pool.PoolStat
	servers  []pool.ServerConn
	backend  pool.BackendStats
	draining bool
	healthy  bool
}

func (c *consoleBackend) Address() string                { return c.addr }
func (c *consoleBackend) Stats() pool.BackendStats       { return c.backend }
func (c *consoleBackend) PoolStats() []pool.PoolStat     { return c.pools }
func (c *consoleBackend) ServerConns() []pool.ServerConn { return c.servers }
func (c *consoleBackend) IsDraining() bool               { return c.draining }
func (c *consoleBackend) IsHealthy() bool                { return c.healthy }

// consoleFrame is one message the console sent.
type consoleFrame struct {
	tag  byte
	body []byte
}

func (f consoleFrame) values() []string {
	if f.tag != 'D' {
		return nil
	}
	count := int(binary.BigEndian.Uint16(f.body[:2]))
	out := make([]string, 0, count)
	rest := f.body[2:]
	for range count {
		size := int(int32(binary.BigEndian.Uint32(rest[:4])))
		rest = rest[4:]
		if size < 0 {
			out = append(out, "")
			continue
		}
		out = append(out, string(rest[:size]))
		rest = rest[size:]
	}
	return out
}

func testConsole(users []string, authenticated bool, backends ...pool.Backend) *adminConsole {
	return &adminConsole{
		cfg: &config.AdminConsole{
			Enabled: true,
			Users:   users,
		},
		options:       &config.Options{PoolingMode: "transaction", Balancer: "p2c"},
		backends:      func() []pool.Backend { return backends },
		sessions:      newSessionRegistry(),
		stats:         observability.NewDatabaseRegistry(),
		started:       time.Now(),
		authenticated: func() bool { return authenticated },
	}
}

// readUntilReady collects messages up to and including the ReadyForQuery that
// returns the client to the idle state.
func readUntilReady(t *testing.T, conn net.Conn) []consoleFrame {
	t.Helper()

	var out []consoleFrame
	for {
		tag, body, err := protocol.ReadCommand(conn)
		if err != nil {
			t.Fatalf("reading the console reply: %v", err)
		}
		out = append(out, consoleFrame{tag: tag, body: body})
		if tag == 'Z' {
			return out
		}
	}
}

// drive runs one console session, returning the frames each command produced.
func drive(t *testing.T, a *adminConsole, user string, commands ...string) [][]consoleFrame {
	t.Helper()

	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })

	done := make(chan error, 1)
	go func() { done <- a.serve(server, user) }()

	// The startup completion: parameters, backend key, ReadyForQuery.
	readUntilReady(t, client)

	replies := make([][]consoleFrame, 0, len(commands))
	for _, sql := range commands {
		body := append([]byte(sql), 0)
		msg := append([]byte{'Q'}, make([]byte, 4)...)
		binary.BigEndian.PutUint32(msg[1:5], uint32(len(body)+4))
		msg = append(msg, body...)
		if _, err := client.Write(msg); err != nil {
			t.Fatalf("sending %q: %v", sql, err)
		}
		replies = append(replies, readUntilReady(t, client))
	}

	terminate := []byte{'X', 0, 0, 0, 4}
	if _, err := client.Write(terminate); err != nil {
		t.Fatalf("sending Terminate: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, errAdminHandled) {
			t.Fatalf("serve returned %v, want errAdminHandled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after Terminate")
	}
	return replies
}

// The console must not be reachable in passthrough mode. There a backend
// verifies the password, and the console has no backend — so admitting the
// client would mean serving pool and backend inventory to someone whose
// password was never checked by anything.
func TestAdminConsoleRefusesWhenPontusDoesNotAuthenticate(t *testing.T) {
	console := testConsole([]string{"admin"}, false)

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() { done <- console.serve(server, "admin") }()

	// The refusal reaches the client as an error its driver prints, rather than
	// as a closed socket.
	tag, body, err := protocol.ReadCommand(client)
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	if tag != 'E' {
		t.Fatalf("tag = %q, want E (ErrorResponse)", tag)
	}
	if !strings.Contains(string(body), "auth.mode") {
		t.Errorf("refusal does not name the cause: %q", body)
	}

	if err := <-done; !errors.Is(err, errAdminUnavailable) {
		t.Fatalf("serve returned %v, want errAdminUnavailable", err)
	}
}

// A role that authenticated successfully but is not an administrator is told
// so. Authorisation is a separate question from authentication, and answering
// it by dropping the connection would look like a wrong password.
func TestAdminConsoleRefusesARoleThatIsNotListed(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() { done <- console.serve(server, "app_user") }()

	tag, body, err := protocol.ReadCommand(client)
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	if tag != 'E' {
		t.Fatalf("tag = %q, want E (ErrorResponse)", tag)
	}
	if !strings.Contains(string(body), "admin console") {
		t.Errorf("refusal does not name the cause: %q", body)
	}

	if err := <-done; !errors.Is(err, errAdminUnavailable) {
		t.Fatalf("serve returned %v, want errAdminUnavailable", err)
	}
}

func TestAdminConsoleHandlesOnlyItsOwnDatabase(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	if !console.handles("pgbouncer") {
		t.Error("the console does not answer on its default database name")
	}
	if console.handles("orders") {
		t.Error("the console claimed a database belonging to a backend")
	}

	console.cfg.Database = "pontus_admin"
	if console.handles("pgbouncer") {
		t.Error("the console still answers on the default after being renamed")
	}
	if !console.handles("pontus_admin") {
		t.Error("the console does not answer on its configured name")
	}

	// A disabled console claims nothing, whatever it is named.
	console.cfg.Enabled = false
	if console.handles("pontus_admin") {
		t.Error("a disabled console claimed a database")
	}
}

func TestAdminConsoleShowPoolsReportsEachIdentity(t *testing.T) {
	backend := &consoleBackend{
		mockBackend: &mockBackend{},
		addr:        "db1:5432",
		healthy:     true,
		pools: []pool.PoolStat{
			{
				Database: "orders", User: "app",
				Active: 3, Idle: 2, Total: 5, Waiting: 1, MaxConns: 10,
				EmptyAcquires: 4, AcquireWait: 2 * time.Second,
			},
			{Database: "", User: "", Idle: 1, Total: 1, MaxConns: 10},
		},
	}

	replies := drive(t, testConsole([]string{"admin"}, true, backend), "admin", "SHOW POOLS;")

	var rows [][]string
	for _, f := range replies[0] {
		if f.tag == 'D' {
			rows = append(rows, f.values())
		}
	}
	if got, want := len(rows), 2; got != want {
		t.Fatalf("got %d rows, want %d", got, want)
	}

	// database, user, cl_active, cl_waiting, sv_active, sv_idle, sv_used,
	// sv_tested, sv_login, maxwait, maxwait_us, pool_mode, backend
	row := rows[0]
	if row[0] != "orders" || row[1] != "app" {
		t.Errorf("identity = %q/%q, want orders/app", row[0], row[1])
	}
	if row[2] != "3" || row[3] != "1" {
		t.Errorf("cl_active/cl_waiting = %q/%q, want 3/1", row[2], row[3])
	}
	if row[4] != "3" || row[5] != "2" {
		t.Errorf("sv_active/sv_idle = %q/%q, want 3/2", row[4], row[5])
	}
	// Four waits totalling two seconds is a mean of 500ms — the figure that
	// says a pool is too small, which occupancy alone never does.
	if row[9] != "0" || row[10] != "500000" {
		t.Errorf("maxwait/maxwait_us = %q/%q, want 0/500000", row[9], row[10])
	}
	if row[12] != "db1:5432" {
		t.Errorf("backend = %q, want db1:5432", row[12])
	}

	// Pontus's own probes have no user or database, and an empty pair in a
	// monitoring dashboard reads as a bug rather than as the system identity.
	if rows[1][0] != "pontus_system" || rows[1][1] != "pontus_system" {
		t.Errorf("system identity = %q/%q, want pontus_system", rows[1][0], rows[1][1])
	}
}

func TestAdminConsoleShowDatabasesReportsEachBackend(t *testing.T) {
	backends := []pool.Backend{
		&consoleBackend{
			mockBackend: &mockBackend{}, addr: "db1:5432", healthy: true,
			backend: pool.BackendStats{MaxConns: 20, ActiveConns: 4, IdleConns: 3},
		},
		&consoleBackend{
			mockBackend: &mockBackend{}, addr: "db2:5432", healthy: false, draining: true,
			backend: pool.BackendStats{MaxConns: 20},
		},
	}

	replies := drive(t, testConsole([]string{"admin"}, true, backends...), "admin", "SHOW DATABASES")

	var rows [][]string
	for _, f := range replies[0] {
		if f.tag == 'D' {
			rows = append(rows, f.values())
		}
	}
	if got, want := len(rows), 2; got != want {
		t.Fatalf("got %d rows, want %d", got, want)
	}

	// name, host, port, pool_size, current_connections, pool_mode, role,
	// paused, disabled
	if rows[0][1] != "db1" || rows[0][2] != "5432" {
		t.Errorf("host/port = %q/%q, want db1/5432", rows[0][1], rows[0][2])
	}
	if rows[0][3] != "20" || rows[0][4] != "7" {
		t.Errorf("pool_size/current = %q/%q, want 20/7", rows[0][3], rows[0][4])
	}
	if rows[0][5] != "transaction" {
		t.Errorf("pool_mode = %q, want transaction", rows[0][5])
	}
	if rows[1][7] != "yes" || rows[1][8] != "yes" {
		t.Errorf("paused/disabled = %q/%q, want yes/yes", rows[1][7], rows[1][8])
	}
}

func TestAdminConsoleShowClientsReportsLiveSessions(t *testing.T) {
	console := testConsole([]string{"admin"}, true)
	console.sessions.add("app", "orders", "10.0.0.7:54321")

	replies := drive(t, console, "admin", "SHOW CLIENTS")

	var rows [][]string
	for _, f := range replies[0] {
		if f.tag == 'D' {
			rows = append(rows, f.values())
		}
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("got %d rows, want %d", got, want)
	}
	if rows[0][1] != "app" || rows[0][2] != "orders" {
		t.Errorf("identity = %q/%q, want app/orders", rows[0][1], rows[0][2])
	}
	if rows[0][3] != "10.0.0.7" || rows[0][4] != "54321" {
		t.Errorf("addr/port = %q/%q, want 10.0.0.7/54321", rows[0][3], rows[0][4])
	}
}

// A console that drops the connection on a typo is one an operator stops using.
// An unrecognised command has to leave the session usable.
func TestAdminConsoleKeepsTheSessionAfterAnError(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	replies := drive(t, console, "admin", "SHOW NONSENSE", "SHOW VERSION")

	var sawError bool
	for _, f := range replies[0] {
		if f.tag == 'E' {
			sawError = true
		}
	}
	if !sawError {
		t.Error("an unrecognised command produced no ErrorResponse")
	}

	// The second command still worked, which is the point.
	var rows int
	for _, f := range replies[1] {
		if f.tag == 'D' {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("SHOW VERSION returned %d rows after an error, want 1", rows)
	}
}

// SHOW SERVERS is the counterpart to SHOW CLIENTS: that answers who is connected
// to Pontus, this answers what Pontus is holding open against the database. When
// a pool is full, the second is the question an operator actually has.
func TestAdminConsoleShowServersEnumeratesConnections(t *testing.T) {
	connected := time.Now().Add(-5 * time.Minute)
	backend := &consoleBackend{
		mockBackend: &mockBackend{}, addr: "db1:5432", healthy: true,
		servers: []pool.ServerConn{
			{
				Database: "orders", User: "app", State: "active",
				RemoteAddr: "10.0.0.5:5432", LocalAddr: "10.0.0.9:54321",
				ConnectedAt: connected, LastUsed: connected.Add(time.Minute), UseCount: 42,
			},
			{
				Database: "orders", User: "app", State: "idle",
				RemoteAddr: "10.0.0.5:5432", LocalAddr: "10.0.0.9:54322",
				ConnectedAt: connected.Add(time.Second), UseCount: 1,
			},
			// Pontus's own probe connection, which has no identity of its own.
			{State: "login", RemoteAddr: "10.0.0.5:5432", LocalAddr: "10.0.0.9:54323"},
		},
	}

	replies := drive(t, testConsole([]string{"admin"}, true, backend), "admin", "SHOW SERVERS")

	var rows [][]string
	for _, f := range replies[0] {
		if f.tag == 'D' {
			rows = append(rows, f.values())
		}
	}
	if got, want := len(rows), 3; got != want {
		t.Fatalf("got %d rows, want %d", got, want)
	}

	// type, user, database, state, addr, port, local_addr, local_port,
	// connect_time, request_time, use_count, backend
	//
	// Sorted by database then user then connect time, so the system identity —
	// which has neither — comes first.
	system := rows[0]
	if system[3] != "login" {
		t.Errorf("first row state = %q, want login", system[3])
	}
	if system[1] != "pontus_system" || system[2] != "pontus_system" {
		t.Errorf("system identity = %q/%q, want pontus_system", system[1], system[2])
	}
	// Never used, so there is no request time. The zero time would render as
	// year 1, which reads as data rather than absence.
	if system[9] != "" {
		t.Errorf("request_time for an unused connection = %q, want empty", system[9])
	}

	active := rows[1]
	if active[0] != "S" {
		t.Errorf("type = %q, want S", active[0])
	}
	if active[3] != "active" {
		t.Errorf("state = %q, want active", active[3])
	}
	if active[4] != "10.0.0.5" || active[5] != "5432" {
		t.Errorf("addr/port = %q/%q, want 10.0.0.5/5432", active[4], active[5])
	}
	if active[6] != "10.0.0.9" || active[7] != "54321" {
		t.Errorf("local_addr/port = %q/%q, want 10.0.0.9/54321", active[6], active[7])
	}
	if active[10] != "42" {
		t.Errorf("use_count = %q, want 42", active[10])
	}
	if active[11] != "db1:5432" {
		t.Errorf("backend = %q, want db1:5432", active[11])
	}

	if rows[2][3] != "idle" {
		t.Errorf("third row state = %q, want idle", rows[2][3])
	}
}

// A backend with nothing open reports nothing, rather than a row of zeros that
// would read as a connection.
func TestAdminConsoleShowServersIsEmptyWithNoConnections(t *testing.T) {
	backend := &consoleBackend{mockBackend: &mockBackend{}, addr: "db1:5432", healthy: true}

	replies := drive(t, testConsole([]string{"admin"}, true, backend), "admin", "SHOW SERVERS")

	for _, f := range replies[0] {
		if f.tag == 'D' {
			t.Errorf("a backend with no open connections reported a row: %q", f.values())
		}
	}
}

// A driver opening a session probes the server before it does anything else.
// Answering the probes is what lets psql and pgx connect at all.
func TestAdminConsoleAnswersDriverProbes(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	replies := drive(t, console, "admin",
		"SET application_name = 'psql'", "BEGIN", "COMMIT", "")

	for i, reply := range replies {
		for _, f := range reply {
			if f.tag == 'E' {
				t.Errorf("probe %d was refused: %q", i, f.body)
			}
		}
		if last := reply[len(reply)-1]; last.tag != 'Z' {
			t.Errorf("probe %d did not end with ReadyForQuery", i)
		}
	}
}

// tagged builds one protocol message.
func tagged(tag byte, body []byte) []byte {
	msg := make([]byte, 5, 5+len(body))
	msg[0] = tag
	binary.BigEndian.PutUint32(msg[1:5], uint32(len(body)+4))
	return append(msg, body...)
}

func cstring(s string) []byte { return append([]byte(s), 0) }

// The extended protocol is what pgx, the JDBC driver and most client libraries
// use by default. A console that answers only simple queries is one that works
// when a person tries it by hand and fails from every program.
func TestAdminConsoleAnswersTheExtendedProtocol(t *testing.T) {
	backend := &consoleBackend{
		mockBackend: &mockBackend{}, addr: "db1:5432", healthy: true,
		pools: []pool.PoolStat{{Database: "orders", User: "app", Active: 2, Idle: 1, MaxConns: 10}},
	}
	console := testConsole([]string{"admin"}, true, backend)

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() { done <- console.serve(server, "admin") }()
	readUntilReady(t, client)

	var batch []byte
	// Parse: statement name, query, no parameters.
	parse := append(cstring("stmt"), cstring("SHOW POOLS")...)
	parse = binary.BigEndian.AppendUint16(parse, 0)
	batch = append(batch, tagged('P', parse)...)
	// Describe the statement, which asks for its parameters and row shape.
	batch = append(batch, tagged('D', append([]byte{'S'}, cstring("stmt")...))...)
	// Bind: unnamed portal, no formats, no parameters, no result formats.
	bind := append(cstring(""), cstring("stmt")...)
	bind = binary.BigEndian.AppendUint16(bind, 0)
	bind = binary.BigEndian.AppendUint16(bind, 0)
	bind = binary.BigEndian.AppendUint16(bind, 0)
	batch = append(batch, tagged('B', bind)...)
	// Execute the portal with no row limit, then Sync.
	execute := append(cstring(""), 0, 0, 0, 0)
	batch = append(batch, tagged('E', execute)...)
	batch = append(batch, tagged('S', nil)...)

	go func() { _, _ = client.Write(batch) }()

	var tags []byte
	var rows int
	for _, f := range readUntilReady(t, client) {
		tags = append(tags, f.tag)
		if f.tag == 'D' {
			rows++
			if values := f.values(); values[0] != "orders" {
				t.Errorf("row database = %q, want orders", values[0])
			}
		}
	}

	// ParseComplete, ParameterDescription, RowDescription, BindComplete, one
	// DataRow, CommandComplete, ReadyForQuery.
	if got, want := string(tags), "1tT2DCZ"; got != want {
		t.Errorf("message sequence = %q, want %q", got, want)
	}
	if rows != 1 {
		t.Errorf("got %d rows, want 1", rows)
	}

	_ = client.Close()
	<-done
}

// After an error the protocol requires the server to skip every message until
// Sync. A client that sent a batch is entitled to have the rest of it ignored
// rather than half-executed.
func TestAdminConsoleSkipsABatchAfterAnError(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() { done <- console.serve(server, "admin") }()
	readUntilReady(t, client)

	var batch []byte
	parse := append(cstring(""), cstring("SHOW NONSENSE")...)
	parse = binary.BigEndian.AppendUint16(parse, 0)
	batch = append(batch, tagged('P', parse)...)
	batch = append(batch, tagged('D', append([]byte{'S'}, cstring("")...))...)
	// Nothing should answer this, because the Describe above failed.
	batch = append(batch, tagged('E', append(cstring(""), 0, 0, 0, 0))...)
	batch = append(batch, tagged('S', nil)...)

	go func() { _, _ = client.Write(batch) }()

	var tags []byte
	for _, f := range readUntilReady(t, client) {
		tags = append(tags, f.tag)
	}

	// ParseComplete, the ErrorResponse from Describe, then ReadyForQuery from
	// Sync — with nothing in between, because Execute was skipped.
	if got, want := string(tags), "1EZ"; got != want {
		t.Errorf("message sequence = %q, want %q", got, want)
	}

	_ = client.Close()
	<-done
}

func TestParseCommandStripsWhatAnOperatorTypes(t *testing.T) {
	for _, tc := range []struct {
		in       string
		verb     string
		argument string
	}{
		{"SHOW POOLS;", "SHOW", "POOLS"},
		{"  show   pools  ; ", "SHOW", "POOLS"},
		{"show pools", "SHOW", "POOLS"},
		{"SHOW", "SHOW", ""},
		{"", "", ""},
		{"   ;  ", "", ""},
	} {
		verb, argument := parseCommand(tc.in)
		if verb != tc.verb || argument != tc.argument {
			t.Errorf("parseCommand(%q) = %q/%q, want %q/%q",
				tc.in, verb, argument, tc.verb, tc.argument)
		}
	}
}

func TestSessionRegistryIsBoundedByLiveConnections(t *testing.T) {
	r := newSessionRegistry()

	first := r.add("app", "orders", "10.0.0.1:100")
	second := r.add("app", "billing", "10.0.0.2:200")
	if got, want := r.count(), 2; got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}

	// Ordered by acceptance, so repeated reads are stable rather than in map
	// order.
	list := r.list()
	if list[0].id != first || list[1].id != second {
		t.Error("sessions are not ordered by the sequence they were accepted in")
	}

	r.remove(first)
	r.remove(second)
	if got := r.count(); got != 0 {
		t.Fatalf("count = %d after every session closed, want 0", got)
	}
}

// SHOW STATS reports what a database has actually done, so a stub that returned
// zeros would pass any test that only checks the columns. This one records
// through the same counters the query path uses and reads them back.
func TestAdminConsoleShowStatsReportsRecordedWork(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	orders := console.stats.For("orders")
	orders.RecordQuery(20*time.Millisecond, 100, 900, true)
	orders.RecordQuery(10*time.Millisecond, 50, 450, false)
	console.stats.For("billing").RecordQuery(5*time.Millisecond, 10, 20, true)

	replies := drive(t, console, "admin", "SHOW STATS")

	rows := map[string][]string{}
	for _, f := range replies[0] {
		if f.tag == 'D' {
			values := f.values()
			rows[values[0]] = values
		}
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(rows), rows)
	}

	// database, total_xact_count, total_query_count, total_received, total_sent,
	// total_query_time, total_wait_time, then the averages.
	row := rows["orders"]
	if row[1] != "1" {
		t.Errorf("total_xact_count = %q, want 1 — only one statement ended a transaction", row[1])
	}
	if row[2] != "2" {
		t.Errorf("total_query_count = %q, want 2", row[2])
	}
	if row[3] != "150" || row[4] != "1350" {
		t.Errorf("received/sent = %q/%q, want 150/1350", row[3], row[4])
	}
	// Microseconds, as pgbouncer reports them.
	if row[5] != "30000" {
		t.Errorf("total_query_time = %q, want 30000 microseconds", row[5])
	}
	// avg_query_time is the mean over the statements, not over the window.
	if row[11] != "15000" {
		t.Errorf("avg_query_time = %q, want 15000 microseconds", row[11])
	}

	if rows["billing"][2] != "1" {
		t.Errorf("billing total_query_count = %q, want 1", rows["billing"][2])
	}
}

// The database name comes from a startup packet, so the registry is a map keyed
// by client-supplied input. Past its bound everything has to land in one bucket
// rather than growing without limit.
func TestDatabaseStatsAreBounded(t *testing.T) {
	registry := observability.NewDatabaseRegistry()

	for i := range observability.MaxTrackedDatabases + 50 {
		registry.For(fmt.Sprintf("db%d", i)).RecordQuery(time.Millisecond, 1, 1, true)
	}

	snapshot := registry.Snapshot()
	if len(snapshot) > observability.MaxTrackedDatabases+1 {
		t.Errorf("registry grew to %d entries; the bound is %d plus the overflow bucket",
			len(snapshot), observability.MaxTrackedDatabases)
	}

	var overflow *observability.DatabaseStat
	var total int64
	for i := range snapshot {
		total += snapshot[i].Queries
		if snapshot[i].Database == observability.OverflowDatabase {
			overflow = &snapshot[i]
		}
	}
	if overflow == nil {
		t.Fatal("nothing past the bound was accounted for")
	}
	if overflow.Queries != 50 {
		t.Errorf("overflow holds %d queries, want the 50 past the bound", overflow.Queries)
	}
	// The totals stay right even though the attribution stops.
	if want := int64(observability.MaxTrackedDatabases + 50); total != want {
		t.Errorf("total queries = %d, want %d", total, want)
	}
}
