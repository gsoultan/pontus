package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/pkg/version"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// errAdminHandled reports that the connection carried an administration console
// session, which is complete. Like a cancel request it never reaches the pool,
// so the caller must not treat the absence of a backend as a failure.
var errAdminHandled = errors.New("admin console session handled")

// errAdminUnavailable reports a console reached in a configuration that cannot
// authenticate the client asking for it.
var errAdminUnavailable = errors.New("admin console unavailable")

// adminConsole answers pgbouncer's administration commands on Pontus's own
// proxy port.
//
// It exists for migration rather than for novelty. A deployment replacing
// pgbouncer already has exporters scraping SHOW POOLS, dashboards built on its
// column names and runbooks that tell an operator to run SHOW DATABASES; a
// pooler that cannot answer them is not a drop-in replacement however good its
// own dashboard is.
//
// The console never touches a backend. Every answer comes from Pontus's own
// state, which is what makes it useful during exactly the incident where the
// database is unreachable.
type adminConsole struct {
	cfg      *config.AdminConsole
	options  *config.Options
	backends func() []pool2.Backend
	sessions *sessionRegistry
	started  time.Time

	// authenticated reports whether Pontus verifies client passwords itself.
	// In passthrough mode a backend does, and the console has no backend to
	// ask — so it must refuse rather than admit an unverified client.
	//
	// Asked at session time rather than captured, because the credential store
	// is installed after the gateway is built: a captured value would be false
	// for every session no matter how auth was configured.
	authenticated func() bool
}

// handles reports whether a startup naming this database belongs to the
// console rather than to a backend.
func (a *adminConsole) handles(database string) bool {
	return a != nil && a.cfg != nil && a.cfg.Enabled &&
		database == a.cfg.DatabaseName()
}

// serve runs one console session to completion.
//
// The client has been authenticated and has had AuthenticationOk; what it is
// waiting for is the rest of the startup — the parameters, the backend key and
// ReadyForQuery — which normally arrive from a backend and here have to be
// supplied.
func (a *adminConsole) serve(client net.Conn, user string) error {
	// Authorization is separate from authentication and checked here rather
	// than at the door, because a role that authenticated successfully but is
	// not an administrator must be told so, not disconnected as if its
	// password were wrong.
	if a.authenticated == nil || !a.authenticated() {
		_ = protocol.WriteClientError(client, "0A000",
			"the admin console requires auth.mode: pontus, because in passthrough "+
				"mode a backend verifies the password and the console has no backend")
		return fmt.Errorf("%w: passthrough mode cannot authenticate a console client",
			errAdminUnavailable)
	}
	if !a.cfg.Permits(user) {
		_ = protocol.WriteClientError(client, "42501",
			"this role may not use the admin console")
		return fmt.Errorf("%w: role %q is not listed in admin_console.users",
			errAdminUnavailable, user)
	}

	if err := protocol.CompleteClientStartup(client, a.startup()); err != nil {
		return err
	}

	// Both protocols, because which one a session uses is the driver's choice
	// and not the operator's. psql sends a simple query; pgx, the JDBC driver
	// and most other client libraries default to Parse/Bind/Execute, and a
	// console that answers only the first is one that works when a person tries
	// it by hand and fails from every program.
	var portal extendedPortal
	for {
		tag, body, err := protocol.ReadCommand(client)
		if err != nil {
			// A client that closes without Terminate is ordinary, not a fault.
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return errAdminHandled
			}
			return err
		}

		if tag == 'X' {
			return errAdminHandled
		}
		if err := a.dispatch(client, &portal, tag, body); err != nil {
			return err
		}
	}
}

// extendedPortal carries what the extended protocol spreads across messages:
// the statement named by Parse, the reply it produces, and whether the exchange
// has failed and must be skipped until Sync.
type extendedPortal struct {
	query    string
	prepared *consoleReply
	failed   bool

	// formats are the result format codes Bind asked for. A driver decodes by
	// what it requested, so describing text and then sending binary is a
	// corrupt row rather than a mismatch it recovers from.
	formats []int16
}

// dispatch answers one message.
func (a *adminConsole) dispatch(client net.Conn, portal *extendedPortal, tag byte, body []byte) error {
	// After an error the server skips every message until Sync. A client that
	// sent a batch is entitled to have the rest of it ignored rather than
	// half-executed.
	if portal.failed && tag != 'S' {
		return nil
	}

	switch tag {
	case 'Q':
		return a.simpleQuery(client, protocol.QueryText(body))

	case 'P':
		parsed, err := protocol.DecodeParse(body)
		if err != nil {
			return a.failExtended(client, portal, "08P01", err.Error())
		}
		portal.query = parsed.Query
		portal.prepared = nil
		portal.formats = nil
		return protocol.WriteAck(client, protocol.TagParseComplete)

	case 'B':
		// Parameter values are not read: nothing the console answers takes one,
		// and binding a value into a command that has no placeholder would be
		// inventing a meaning for it. The result formats are read, because they
		// decide how the rows must be encoded.
		formats, err := protocol.DecodeBindResultFormats(body)
		if err != nil {
			return a.failExtended(client, portal, "08P01", err.Error())
		}
		portal.formats = formats
		return protocol.WriteAck(client, protocol.TagBindComplete)

	case 'D':
		return a.describe(client, portal, protocol.DescribeTarget(body))

	case 'E':
		return a.execute(client, portal)

	case 'C':
		return protocol.WriteAck(client, protocol.TagCloseComplete)

	case 'H':
		// Every reply is written as it is produced, so there is nothing held
		// back for a Flush to release.
		return nil

	case 'S':
		portal.failed = false
		return protocol.WriteReadyForQuery(client)

	default:
		return a.failExtended(client, portal, "0A000",
			"the admin console does not answer message type "+string(rune(tag)))
	}
}

// prepare produces the reply for the portal's statement, once.
//
// Describe and Execute both need it, and a console command is cheap but not
// free: SHOW POOLS walks every pool on every backend, and doing that twice for
// one round trip would make the console's cost depend on the client's protocol.
func (a *adminConsole) prepare(portal *extendedPortal) (*consoleReply, error) {
	if portal.prepared != nil {
		return portal.prepared, nil
	}

	reply, err := a.answer(portal.query)
	if err != nil {
		return nil, err
	}
	portal.prepared = &reply
	return portal.prepared, nil
}

func (a *adminConsole) describe(client net.Conn, portal *extendedPortal, target byte) error {
	reply, err := a.prepare(portal)
	if err != nil {
		return a.failExtended(client, portal, errorCode(err), err.Error())
	}

	// Describe on a statement reports the parameters before the row shape.
	// Nothing here takes one, which is still an answer and not a refusal.
	if target == 'S' {
		if err := protocol.WriteParameterDescription(client, 0); err != nil {
			return err
		}
	}

	if reply.rows == nil {
		return protocol.WriteAck(client, protocol.TagNoData)
	}
	return reply.rows.Description(client, portal.formats)
}

func (a *adminConsole) execute(client net.Conn, portal *extendedPortal) error {
	reply, err := a.prepare(portal)
	if err != nil {
		return a.failExtended(client, portal, errorCode(err), err.Error())
	}

	if reply.rows == nil {
		return protocol.WriteCommandTag(client, reply.tag)
	}
	return reply.rows.SendRows(client, portal.formats)
}

// failExtended reports an error and puts the exchange into the skip-until-Sync
// state the protocol requires.
func (a *adminConsole) failExtended(client net.Conn, portal *extendedPortal, code, message string) error {
	portal.failed = true
	_, err := client.Write(protocol.ErrorResponse("ERROR", code, message))
	return err
}

// simpleQuery answers a 'Q' message, which carries its own ReadyForQuery.
func (a *adminConsole) simpleQuery(client net.Conn, sql string) error {
	reply, err := a.answer(sql)
	if err != nil {
		// A console that drops the connection on a typo is one an operator
		// stops using, so the session continues.
		if _, werr := client.Write(protocol.ErrorResponse("ERROR", errorCode(err), err.Error())); werr != nil {
			return werr
		}
		return protocol.WriteCommandComplete(client, "SHOW")
	}

	if reply.rows == nil {
		if reply.tag == "" {
			return protocol.WriteEmptyQuery(client)
		}
		return protocol.WriteCommandComplete(client, reply.tag)
	}
	return reply.rows.Send(client)
}

// startup is the set of parameters the console reports in place of a backend's.
//
// A driver reads server_version to decide which syntax it may use. Reporting a
// PostgreSQL version keeps libpq and pgx from refusing the connection outright,
// and the pontus suffix keeps anything that logs it honest about what answered.
func (a *adminConsole) startup() *protocol.Startup {
	return &protocol.Startup{
		Params: map[string]string{
			"server_version":  "14.0 (pontus " + version.Version + ")",
			"server_encoding": "UTF8",
			"client_encoding": "UTF8",
			"DateStyle":       "ISO, MDY",
			"TimeZone":        "UTC",
			"is_superuser":    "off",
			// An administration console has no transactions to be read-write
			// about, and a driver that probes this expects an answer.
			"default_transaction_read_only": "on",
			"in_hot_standby":                "off",
			"integer_datetimes":             "on",
			"standard_conforming_strings":   "on",
			"application_name":              "pontus-admin",
		},
		// BackendKeyData is not optional in practice: a client that models the
		// startup sequence strictly treats its absence as a protocol error.
		// There is no process to cancel, so the key cancels nothing — it exists
		// so the sequence is well formed.
		BackendKey: make([]byte, 8),
	}
}

// consoleReply is what a command produced: a result set, or a command tag with
// no rows. Built rather than written, because the extended protocol asks for
// the shape and the data at different moments.
type consoleReply struct {
	rows *protocol.ResultSet
	tag  string
}

// consoleError carries the SQLSTATE a refusal should reach the client as.
type consoleError struct {
	code    string
	message string
}

func (e *consoleError) Error() string { return e.message }

func refuse(code, message string) error { return &consoleError{code: code, message: message} }

// errorCode is the SQLSTATE to report an error under. Anything without one is
// an internal fault rather than a bad command.
func errorCode(err error) string {
	var ce *consoleError
	if errors.As(err, &ce) {
		return ce.code
	}
	return "XX000"
}

// answer runs one command.
func (a *adminConsole) answer(sql string) (consoleReply, error) {
	verb, argument := parseCommand(sql)

	switch verb {
	case "":
		return consoleReply{}, nil

	case "SHOW":
		return a.show(argument)

	// A driver connecting to any PostgreSQL server probes it before it does
	// anything else. Answering the probes rather than erroring is what lets
	// psql and pgx open a session at all, which is the difference between a
	// console an operator can use and one they can only reach with a raw
	// socket.
	case "SET", "BEGIN", "COMMIT", "ROLLBACK", "DISCARD", "RESET":
		return consoleReply{tag: verb}, nil

	default:
		return consoleReply{}, refuse("42601", "unrecognised console command: "+verb+
			"; SHOW HELP lists what this console answers")
	}
}

// parseCommand splits a statement into its leading verb and the rest.
//
// Trailing semicolons and surrounding whitespace are stripped because a person
// types them and psql forwards them verbatim.
func parseCommand(sql string) (verb, argument string) {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";"))
	if trimmed == "" {
		return "", ""
	}

	first, rest, _ := strings.Cut(trimmed, " ")
	return strings.ToUpper(first), strings.ToUpper(strings.TrimSpace(rest))
}

func (a *adminConsole) show(what string) (consoleReply, error) {
	switch what {
	case "POOLS":
		return rowsOf(a.showPools()), nil
	case "DATABASES":
		return rowsOf(a.showDatabases()), nil
	case "CLIENTS":
		return rowsOf(a.showClients()), nil
	case "LISTS":
		return rowsOf(a.showLists()), nil
	case "CONFIG":
		return rowsOf(a.showConfig()), nil
	case "VERSION":
		return rowsOf(a.showVersion()), nil
	case "HELP", "":
		return rowsOf(a.showHelp()), nil

	// Named rather than folded into the default, because "unrecognised" would
	// send an operator looking for a typo in a command that is simply not
	// implemented yet. Both need counters Pontus does not keep: per-database
	// query and byte totals for STATS, and per-connection server detail for
	// SERVERS.
	case "STATS", "STATS_TOTALS", "STATS_AVERAGES", "TOTALS", "SERVERS":
		return consoleReply{}, refuse("0A000", "SHOW "+what+" is not implemented yet: "+
			"Pontus does not keep the counters it reports; SHOW POOLS carries the "+
			"occupancy and wait figures that are available")

	default:
		return consoleReply{}, refuse("42601", "unrecognised: SHOW "+what+
			"; SHOW HELP lists what this console answers")
	}
}

func rowsOf(rs *protocol.ResultSet) consoleReply { return consoleReply{rows: rs} }

// showPools reports occupancy per (database, user), which is how Pontus's pools
// are keyed.
//
// The column names are pgbouncer's so that existing exporters read them. Where
// a column names a state Pontus does not have — a server connection being
// tested, or logging in — it is reported as zero, which is accurate rather than
// a placeholder: those states do not exist here.
func (a *adminConsole) showPools() *protocol.ResultSet {
	rs := protocol.NewResultSet(
		protocol.TextColumn("database"),
		protocol.TextColumn("user"),
		protocol.NumericColumn("cl_active"),
		protocol.NumericColumn("cl_waiting"),
		protocol.NumericColumn("sv_active"),
		protocol.NumericColumn("sv_idle"),
		protocol.NumericColumn("sv_used"),
		protocol.NumericColumn("sv_tested"),
		protocol.NumericColumn("sv_login"),
		protocol.NumericColumn("maxwait"),
		protocol.NumericColumn("maxwait_us"),
		protocol.TextColumn("pool_mode"),
		protocol.TextColumn("backend"),
	)

	mode := a.poolMode()
	for _, backend := range a.backends() {
		stats, ok := backend.(interface{ PoolStats() []pool2.PoolStat })
		if !ok {
			continue
		}
		for _, p := range stats.PoolStats() {
			// A client holds a server connection only while it is executing, so
			// in Pontus's transaction pooling the two counts are the same
			// quantity seen from either end rather than two measurements.
			wait := p.AverageWait()
			rs.Row(
				orSystem(p.Database), orSystem(p.User),
				itoa(int64(p.Active)),
				itoa(int64(p.Waiting)),
				itoa(int64(p.Active)),
				itoa(int64(p.Idle)),
				"0", "0", "0",
				itoa(int64(wait/time.Second)),
				itoa(int64(wait/time.Microsecond)),
				mode,
				backend.Address(),
			)
		}
	}
	return rs
}

// showDatabases reports one row per configured backend.
//
// pgbouncer lists the entries of its [databases] section, which Pontus has no
// equivalent of: a client names its database in the startup packet and Pontus
// routes it to a backend by role and health. A backend is the closest honest
// row, and the extra role and health columns say so rather than leaving an
// operator to infer it.
func (a *adminConsole) showDatabases() *protocol.ResultSet {
	rs := protocol.NewResultSet(
		protocol.TextColumn("name"),
		protocol.TextColumn("host"),
		protocol.NumericColumn("port"),
		protocol.NumericColumn("pool_size"),
		protocol.NumericColumn("current_connections"),
		protocol.TextColumn("pool_mode"),
		protocol.TextColumn("role"),
		protocol.TextColumn("paused"),
		protocol.TextColumn("disabled"),
	)

	mode := a.poolMode()
	for _, backend := range a.backends() {
		host, port := splitHostPort(backend.Address())
		st := backend.Stats()
		rs.Row(
			backend.Address(),
			host,
			port,
			itoa(int64(st.MaxConns)),
			itoa(int64(st.ActiveConns+st.IdleConns)),
			mode,
			string(backend.Role()),
			yesNo(backend.IsDraining()),
			yesNo(!backend.IsHealthy()),
		)
	}
	return rs
}

// showClients reports the live client sessions.
func (a *adminConsole) showClients() *protocol.ResultSet {
	rs := protocol.NewResultSet(
		protocol.TextColumn("type"),
		protocol.TextColumn("user"),
		protocol.TextColumn("database"),
		protocol.TextColumn("addr"),
		protocol.NumericColumn("port"),
		protocol.TextColumn("connect_time"),
		protocol.NumericColumn("connect_age_seconds"),
	)

	now := time.Now()
	for _, s := range a.sessions.list() {
		host, port := splitHostPort(s.addr)
		rs.Row(
			"C",
			s.user,
			s.database,
			host,
			port,
			s.since.UTC().Format(time.RFC3339),
			itoa(int64(now.Sub(s.since)/time.Second)),
		)
	}
	return rs
}

// showLists reports the size of each of Pontus's internal collections.
func (a *adminConsole) showLists() *protocol.ResultSet {
	var pools int
	backends := a.backends()
	for _, backend := range backends {
		if stats, ok := backend.(interface{ PoolStats() []pool2.PoolStat }); ok {
			pools += len(stats.PoolStats())
		}
	}

	rs := protocol.NewResultSet(
		protocol.TextColumn("list"),
		protocol.NumericColumn("items"),
	)
	rs.Row("backends", itoa(int64(len(backends))))
	rs.Row("pools", itoa(int64(pools)))
	rs.Row("clients", itoa(int64(a.sessions.count())))
	return rs
}

// showConfig reports the settings that govern the data path.
//
// Deliberately not every field of the configuration: this is reachable by an
// administrative role rather than by the machine's operator, and secrets,
// tokens and file paths are not something the console needs to disclose in
// order to be useful.
func (a *adminConsole) showConfig() *protocol.ResultSet {
	rs := protocol.NewResultSet(
		protocol.TextColumn("key"),
		protocol.TextColumn("value"),
		protocol.TextColumn("changeable"),
	)

	cfg := a.options
	if cfg == nil {
		cfg = &config.Options{}
	}

	rs.Row("pooling_mode", a.poolMode(), "yes")
	rs.Row("balancer", cfg.Balancer, "yes")
	rs.Row("max_conns", itoa(int64(cfg.MaxConns)), "yes")
	rs.Row("min_idle", itoa(int64(cfg.MinIdle)), "yes")
	rs.Row("query_timeout", cfg.QueryTimeout.String(), "yes")
	rs.Row("pool_wait_timeout", cfg.PoolWaitTimeout.String(), "yes")
	rs.Row("dial_timeout", cfg.DialTimeout.String(), "yes")
	rs.Row("health_interval", cfg.HealthInterval.String(), "yes")
	rs.Row("local_zone", cfg.LocalZone, "yes")
	rs.Row("admin_console.database", a.cfg.DatabaseName(), "no")
	return rs
}

func (a *adminConsole) showVersion() *protocol.ResultSet {
	rs := protocol.NewResultSet(protocol.TextColumn("version"))
	rs.Row("Pontus " + version.Version + " (commit " + version.Commit + ")")
	return rs
}

func (a *adminConsole) showHelp() *protocol.ResultSet {
	rs := protocol.NewResultSet(
		protocol.TextColumn("command"),
		protocol.TextColumn("description"),
	)
	rs.Row("SHOW POOLS", "connection pool occupancy per database and user")
	rs.Row("SHOW DATABASES", "configured backends, their role and their ceiling")
	rs.Row("SHOW CLIENTS", "live client sessions")
	rs.Row("SHOW LISTS", "size of each internal collection")
	rs.Row("SHOW CONFIG", "settings governing the data path")
	rs.Row("SHOW VERSION", "the running Pontus build")
	rs.Row("SHOW HELP", "this list")
	return rs
}

// poolMode names the pooling mode in pgbouncer's vocabulary.
func (a *adminConsole) poolMode() string {
	if a.options != nil && a.options.PoolingMode != "" {
		return a.options.PoolingMode
	}
	return "transaction"
}

// orSystem names the identity Pontus uses for its own health probes and role
// detection, which has no user or database of its own.
func orSystem(value string) string {
	if value == "" {
		return "pontus_system"
	}
	return value
}

func splitHostPort(addr string) (host, port string) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, "0"
	}
	return h, p
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// newAdminConsole builds the console for a configuration, or nil when it is not
// enabled.
func (g *Gateway) newAdminConsole(cfg *config.Options) *adminConsole {
	if cfg == nil || cfg.AdminConsole == nil || !cfg.AdminConsole.Enabled {
		return nil
	}

	return &adminConsole{
		cfg:      cfg.AdminConsole,
		options:  cfg,
		backends: g.backendList,
		sessions: g.sessions,
		started:  time.Now(),

		authenticated: func() bool { return g.credentials != nil },
	}
}

// backendList returns this proxy's backends, or nothing when no supplier has
// been set. Nothing rather than a panic: a gateway built without one — every
// unit test does — must still serve.
func (g *Gateway) backendList() []pool2.Backend {
	if g.backends == nil {
		return nil
	}
	return g.backends()
}

// SetBackends supplies the backends the administration console reports on.
//
// A supplier rather than a slice because the registry builds the backends after
// the gateway, which is why the failover manager takes one too.
func (g *Gateway) SetBackends(backends func() []pool2.Backend) {
	g.backends = backends
}
