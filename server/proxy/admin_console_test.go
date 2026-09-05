package proxy

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// consoleBackend is a backend that reports pool occupancy. It embeds the
// package's existing double so it only has to say what the console reads.
type consoleBackend struct {
	*mockBackend
	addr     string
	pools    []pool.PoolStat
	backend  pool.BackendStats
	draining bool
	healthy  bool
}

func (c *consoleBackend) Address() string            { return c.addr }
func (c *consoleBackend) Stats() pool.BackendStats   { return c.backend }
func (c *consoleBackend) PoolStats() []pool.PoolStat { return c.pools }
func (c *consoleBackend) IsDraining() bool           { return c.draining }
func (c *consoleBackend) IsHealthy() bool            { return c.healthy }

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
	t.Cleanup(func() { client.Close() })

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
	defer client.Close()

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
	defer client.Close()

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

// SHOW STATS and SHOW SERVERS need counters Pontus does not keep. Reporting
// zeros would put "0 queries/sec" on a dashboard forever, which looks like a
// working integration; saying so does not.
func TestAdminConsoleSaysWhatItDoesNotImplement(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	for _, command := range []string{"SHOW STATS", "SHOW SERVERS"} {
		replies := drive(t, console, "admin", command)

		var message string
		for _, f := range replies[0] {
			if f.tag == 'E' {
				message = string(f.body)
			}
			if f.tag == 'D' {
				t.Errorf("%s returned a row rather than saying it is unimplemented", command)
			}
		}
		if !strings.Contains(message, "not implemented") {
			t.Errorf("%s did not say it is unimplemented: %q", command, message)
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
	defer client.Close()

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

	go func() { client.Write(batch) }()

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

	client.Close()
	<-done
}

// After an error the protocol requires the server to skip every message until
// Sync. A client that sent a batch is entitled to have the rest of it ignored
// rather than half-executed.
func TestAdminConsoleSkipsABatchAfterAnError(t *testing.T) {
	console := testConsole([]string{"admin"}, true)

	client, server := net.Pipe()
	defer client.Close()

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

	go func() { client.Write(batch) }()

	var tags []byte
	for _, f := range readUntilReady(t, client) {
		tags = append(tags, f.tag)
	}

	// ParseComplete, the ErrorResponse from Describe, then ReadyForQuery from
	// Sync — with nothing in between, because Execute was skipped.
	if got, want := string(tags), "1EZ"; got != want {
		t.Errorf("message sequence = %q, want %q", got, want)
	}

	client.Close()
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
