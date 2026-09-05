package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// Column type OIDs. Only the two Pontus reports its own rows in.
const (
	oidInt8 = 20
	oidText = 25
)

// Column is one column of a result set Pontus produces itself, rather than
// relaying from a backend.
type Column struct {
	Name string
	OID  uint32
}

// TextColumn describes a column carrying a string.
func TextColumn(name string) Column { return Column{Name: name, OID: oidText} }

// NumericColumn describes a column carrying an integer.
//
// The value still travels as text — format code 0 — because that is what
// PostgreSQL sends in reply to a simple query. Declaring the OID anyway is what
// lets a driver hand its caller an int64 instead of a string, which is the
// difference between a monitoring exporter reading a gauge and reporting every
// number it collects as an unparseable string.
func NumericColumn(name string) Column { return Column{Name: name, OID: oidInt8} }

// ResultSet builds the reply to a query Pontus answers by itself.
//
// The administration console has no backend to relay, so the rows have to be
// encoded here. A simple-query reply is RowDescription, one DataRow per row,
// CommandComplete and then ReadyForQuery; a client that receives them in any
// other order reports a protocol violation rather than a missing feature, so
// the order is the type's responsibility and not the caller's.
//
// Every message is accumulated and written once. A reply built from a dozen
// small writes is a dozen syscalls and, on a connection a client is waiting
// on, a dozen chances to interleave with something else.
type ResultSet struct {
	columns []Column

	// Rows are kept as values rather than as encoded bytes, because the client
	// chooses the encoding after the rows exist. In the extended protocol Bind
	// asks for binary for the types its driver knows how to decode that way,
	// and that message arrives after Describe — so a set encoded at build time
	// is encoded before anyone has said which encoding to use.
	rows [][]string

	err error
}

// NewResultSet starts a result set with the given columns.
func NewResultSet(columns ...Column) *ResultSet {
	return &ResultSet{columns: columns}
}

// Row appends one row. The number of values must match the number of columns:
// a mismatch is a protocol violation that reaches the client as a corrupt
// stream, so it is recorded here and reported by Send instead.
func (r *ResultSet) Row(values ...string) {
	if len(values) != len(r.columns) {
		if r.err == nil {
			r.err = fmt.Errorf("result set has %d columns but the row carries %d values",
				len(r.columns), len(values))
		}
		return
	}
	r.rows = append(r.rows, slices.Clone(values))
}

// Send writes the whole reply to a simple query: the row shape, the rows, the
// CommandComplete that ends them and the ReadyForQuery that returns the client
// to the idle state.
//
// A simple query has no Bind, so every value goes as text. That is not a
// limitation being worked around — it is the only encoding the simple protocol
// has.
func (r *ResultSet) Send(w io.Writer) error {
	if r.err != nil {
		return r.err
	}

	out := appendRowDescription(nil, r.columns, nil)
	out = r.appendRows(out, nil)
	out = appendTagged(out, 'C', commandTag("SELECT", len(r.rows)))
	out = appendTagged(out, 'Z', []byte{txIdle})
	_, err := w.Write(out)
	return err
}

// Description writes the RowDescription alone, which is what the extended
// protocol asks for at Describe — before it has decided to execute anything.
//
// formats are the result format codes the client asked for, which is what the
// declared format in each column must agree with: a driver decodes by what it
// requested, so describing text and then sending binary is a corrupt row rather
// than a mismatch it recovers from.
func (r *ResultSet) Description(w io.Writer, formats []int16) error {
	if r.err != nil {
		return r.err
	}
	_, err := w.Write(appendRowDescription(nil, r.columns, formats))
	return err
}

// SendRows writes the rows and the CommandComplete that ends them, without the
// RowDescription that Describe already sent and without ReadyForQuery.
//
// In the extended protocol ReadyForQuery answers Sync, not Execute: sending one
// here would tell a client the whole exchange is over while it is still in the
// middle of a batch.
func (r *ResultSet) SendRows(w io.Writer, formats []int16) error {
	if r.err != nil {
		return r.err
	}

	out := r.appendRows(nil, formats)
	out = appendTagged(out, 'C', commandTag("SELECT", len(r.rows)))
	_, err := w.Write(out)
	return err
}

func (r *ResultSet) appendRows(dst []byte, formats []int16) []byte {
	for _, values := range r.rows {
		body := make([]byte, 0, rowSizeHint(values))
		body = binary.BigEndian.AppendUint16(body, uint16(len(values)))
		for i, v := range values {
			body = appendValue(body, r.columns[i], v, formatFor(formats, i))
		}
		dst = appendTagged(dst, 'D', body)
	}
	return dst
}

// formatFor is the format code for one column.
//
// The protocol allows three shapes: no codes at all meaning text, exactly one
// meaning it applies to every column, and one per column.
func formatFor(formats []int16, column int) int16 {
	switch {
	case len(formats) == 0:
		return formatText
	case len(formats) == 1:
		return formats[0]
	case column < len(formats):
		return formats[column]
	default:
		return formatText
	}
}

// appendValue encodes one value in the format the client asked for.
func appendValue(dst []byte, column Column, value string, format int16) []byte {
	if format != formatBinary {
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
		return append(dst, value...)
	}

	// Binary is requested for the types a driver has a binary codec for, which
	// here is only int8. Anything else is sent as its bytes, which is what the
	// binary encoding of a text value is.
	if column.OID != oidInt8 {
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
		return append(dst, value...)
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		// A column declared numeric that does not hold a number is this
		// package's bug, not the client's. NULL is the honest answer: it is
		// decodable, where eight bytes of nothing is not.
		return binary.BigEndian.AppendUint32(dst, ^uint32(0))
	}
	dst = binary.BigEndian.AppendUint32(dst, 8)
	return binary.BigEndian.AppendUint64(dst, uint64(n))
}

// Result format codes.
const (
	formatText   int16 = 0
	formatBinary int16 = 1
)

// WriteCommandComplete reports a command that returned no rows, followed by the
// ReadyForQuery a simple query ends with.
func WriteCommandComplete(w io.Writer, tag string) error {
	body := make([]byte, 0, len(tag)+1)
	body = append(body, tag...)
	body = append(body, 0)

	out := appendTagged(nil, 'C', body)
	out = appendTagged(out, 'Z', []byte{txIdle})
	_, err := w.Write(out)
	return err
}

// WriteEmptyQuery answers an empty statement, which a client may send and which
// PostgreSQL answers with EmptyQueryResponse rather than an error.
func WriteEmptyQuery(w io.Writer) error {
	out := appendTagged(nil, 'I', nil)
	out = appendTagged(out, 'Z', []byte{txIdle})
	_, err := w.Write(out)
	return err
}

// txIdle is the ReadyForQuery status for a session outside a transaction. The
// administration console never opens one.
const txIdle = 'I'

func appendRowDescription(dst []byte, columns []Column, formats []int16) []byte {
	body := make([]byte, 0, len(columns)*24)
	body = binary.BigEndian.AppendUint16(body, uint16(len(columns)))
	for i, c := range columns {
		body = append(body, c.Name...)
		body = append(body, 0)
		body = binary.BigEndian.AppendUint32(body, 0) // no table of origin
		body = binary.BigEndian.AppendUint16(body, 0) // no column attribute number
		body = binary.BigEndian.AppendUint32(body, c.OID)
		body = binary.BigEndian.AppendUint16(body, uint16(typeSize(c.OID)))
		body = binary.BigEndian.AppendUint32(body, 0xFFFFFFFF) // no type modifier
		body = binary.BigEndian.AppendUint16(body, uint16(formatFor(formats, i)))
	}
	return appendTagged(dst, 'T', body)
}

// typeSize is the type's on-the-wire width, which is -1 for a variable-length
// type. It is declared in RowDescription even when every value is sent as text.
func typeSize(oid uint32) int16 {
	if oid == oidInt8 {
		return 8
	}
	return -1
}

func commandTag(verb string, rows int) []byte {
	tag := make([]byte, 0, len(verb)+12)
	tag = append(tag, verb...)
	tag = append(tag, ' ')
	tag = appendInt(tag, rows)
	return append(tag, 0)
}

func appendInt(dst []byte, n int) []byte {
	if n == 0 {
		return append(dst, '0')
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return append(dst, digits[i:]...)
}

func appendTagged(dst []byte, tag byte, body []byte) []byte {
	dst = append(dst, tag)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(body)+4))
	return append(dst, body...)
}

func rowSizeHint(values []string) int {
	size := 2
	for _, v := range values {
		size += 4 + len(v)
	}
	return size
}

// Extended-protocol message tags Pontus answers with when it is the server.
const (
	TagParseComplete = '1'
	TagBindComplete  = '2'
	TagCloseComplete = '3'
	TagNoData        = 'n'
)

// WriteAck writes one of the extended protocol's bodyless acknowledgements.
func WriteAck(w io.Writer, tag byte) error {
	_, err := w.Write(appendTagged(nil, tag, nil))
	return err
}

// WriteReadyForQuery returns the client to the idle state.
//
// In the extended protocol this answers Sync rather than Execute: sending it
// after Execute would tell a client the exchange is over while it is still in
// the middle of a batch.
func WriteReadyForQuery(w io.Writer) error {
	_, err := w.Write(appendTagged(nil, 'Z', []byte{txIdle}))
	return err
}

// WriteParameterDescription reports how many parameters a statement takes,
// which Describe on a statement asks for before the row shape.
func WriteParameterDescription(w io.Writer, count int) error {
	body := binary.BigEndian.AppendUint16(nil, uint16(count))
	for range count {
		body = binary.BigEndian.AppendUint32(body, oidText)
	}
	_, err := w.Write(appendTagged(nil, 't', body))
	return err
}

// WriteCommandTag reports a command that returned no rows, without the
// ReadyForQuery that ends a simple query.
func WriteCommandTag(w io.Writer, tag string) error {
	body := make([]byte, 0, len(tag)+1)
	body = append(body, tag...)
	body = append(body, 0)

	_, err := w.Write(appendTagged(nil, 'C', body))
	return err
}

// ParseMessage is a Parse ('P') message: the statement it names and the SQL it
// carries.
type ParseMessage struct {
	Name  string
	Query string
}

// DecodeParse reads a Parse message body.
//
// The body is two null-terminated strings and a parameter count. A truncated
// one is a client fault rather than something to guess at, so it is reported.
func DecodeParse(body []byte) (ParseMessage, error) {
	end := indexByte(body, 0)
	if end < 0 {
		return ParseMessage{}, fmt.Errorf("Parse message has no statement name")
	}
	name := string(body[:end])

	rest := body[end+1:]
	end = indexByte(rest, 0)
	if end < 0 {
		return ParseMessage{}, fmt.Errorf("Parse message has no query")
	}
	return ParseMessage{Name: name, Query: string(rest[:end])}, nil
}

// DescribeTarget reports what a Describe ('D') message asks about: 'S' for a
// prepared statement, 'P' for a portal.
func DescribeTarget(body []byte) byte {
	if len(body) == 0 {
		return 0
	}
	return body[0]
}

// DecodeBindResultFormats reads the result format codes a Bind ('B') message
// asks for.
//
// They sit at the end of the message, past the parameter formats and the
// parameter values, so reaching them means walking the whole body. Worth doing
// rather than assuming text: a driver decodes by what it requested, and pgx
// requests binary for every type it has a binary codec for.
func DecodeBindResultFormats(body []byte) ([]int16, error) {
	rest := body
	for range 2 { // the portal name, then the statement name
		end := indexByte(rest, 0)
		if end < 0 {
			return nil, fmt.Errorf("Bind message is truncated in its names")
		}
		rest = rest[end+1:]
	}

	// Parameter format codes.
	count, rest, err := readInt16(rest)
	if err != nil {
		return nil, err
	}
	if len(rest) < count*2 {
		return nil, fmt.Errorf("Bind message declares %d parameter formats it does not carry", count)
	}
	rest = rest[count*2:]

	// Parameter values, each a length that may be -1 for NULL.
	count, rest, err = readInt16(rest)
	if err != nil {
		return nil, err
	}
	for range count {
		if len(rest) < 4 {
			return nil, fmt.Errorf("Bind message is truncated in its parameters")
		}
		size := int32(binary.BigEndian.Uint32(rest[:4]))
		rest = rest[4:]
		if size < 0 {
			continue
		}
		if len(rest) < int(size) {
			return nil, fmt.Errorf("Bind message declares a %d byte parameter it does not carry", size)
		}
		rest = rest[size:]
	}

	// Result format codes.
	count, rest, err = readInt16(rest)
	if err != nil {
		return nil, err
	}
	if len(rest) < count*2 {
		return nil, fmt.Errorf("Bind message declares %d result formats it does not carry", count)
	}

	formats := make([]int16, count)
	for i := range count {
		formats[i] = int16(binary.BigEndian.Uint16(rest[i*2:]))
	}
	return formats, nil
}

func readInt16(b []byte) (value int, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, fmt.Errorf("message is truncated where a count was expected")
	}
	return int(int16(binary.BigEndian.Uint16(b[:2]))), b[2:], nil
}
