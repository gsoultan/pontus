package protocol

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// frame is one decoded protocol message.
type replyFrame struct {
	tag  byte
	body []byte
}

// decode splits a stream into messages, which is what proves the encoder emits
// well-formed frames: a length that does not cover its own four bytes, or a
// body that runs past the end, shows up here as a short read rather than as a
// client that hangs.
func decode(t *testing.T, raw []byte) []replyFrame {
	t.Helper()

	var out []replyFrame
	for len(raw) > 0 {
		if len(raw) < 5 {
			t.Fatalf("trailing %d bytes do not form a message header", len(raw))
		}
		length := int(binary.BigEndian.Uint32(raw[1:5]))
		if length < 4 || 1+length > len(raw) {
			t.Fatalf("message %q declares length %d but only %d bytes remain",
				raw[0], length, len(raw)-1)
		}
		out = append(out, replyFrame{tag: raw[0], body: raw[5 : 1+length]})
		raw = raw[1+length:]
	}
	return out
}

func TestResultSetEmitsAWellFormedSimpleQueryReply(t *testing.T) {
	rs := NewResultSet(TextColumn("database"), NumericColumn("cl_active"))
	rs.Row("orders", "3")
	rs.Row("billing", "0")

	var buf bytes.Buffer
	if err := rs.Send(&buf); err != nil {
		t.Fatalf("Send: %v", err)
	}

	frames := decode(t, buf.Bytes())
	var tags []byte
	for _, f := range frames {
		tags = append(tags, f.tag)
	}
	// RowDescription, two DataRows, CommandComplete, ReadyForQuery — in that
	// order. A client that receives them in any other reports a protocol
	// violation rather than a missing feature.
	if got, want := string(tags), "TDDCZ"; got != want {
		t.Fatalf("message sequence = %q, want %q", got, want)
	}

	if got := binary.BigEndian.Uint16(frames[0].body[:2]); got != 2 {
		t.Errorf("RowDescription declares %d columns, want 2", got)
	}
	if !bytes.Contains(frames[0].body, []byte("cl_active\x00")) {
		t.Error("RowDescription does not name the cl_active column")
	}

	if got, want := string(frames[3].body), "SELECT 2\x00"; got != want {
		t.Errorf("CommandComplete = %q, want %q", got, want)
	}
	if got, want := string(frames[4].body), "I"; got != want {
		t.Errorf("ReadyForQuery status = %q, want %q", got, want)
	}
}

// A numeric column has to declare its OID even though the value travels as
// text, because that is what lets a driver hand its caller an int64. An
// exporter reading every gauge as an unparseable string is the failure this
// prevents.
func TestResultSetDeclaresColumnTypes(t *testing.T) {
	rs := NewResultSet(TextColumn("name"), NumericColumn("items"))

	var buf bytes.Buffer
	if err := rs.Send(&buf); err != nil {
		t.Fatalf("Send: %v", err)
	}

	body := decode(t, buf.Bytes())[0].body
	// Two bytes of column count, then per column: name, NUL, then 18 bytes of
	// which the OID is at offset 6.
	rest := body[2:]
	name, rest, _ := bytes.Cut(rest, []byte{0})
	if string(name) != "name" {
		t.Fatalf("first column = %q, want name", name)
	}
	if oid := binary.BigEndian.Uint32(rest[6:10]); oid != oidText {
		t.Errorf("text column OID = %d, want %d", oid, oidText)
	}
	if size := int16(binary.BigEndian.Uint16(rest[10:12])); size != -1 {
		t.Errorf("text column size = %d, want -1", size)
	}

	rest = rest[18:]
	name, rest, _ = bytes.Cut(rest, []byte{0})
	if string(name) != "items" {
		t.Fatalf("second column = %q, want items", name)
	}
	if oid := binary.BigEndian.Uint32(rest[6:10]); oid != oidInt8 {
		t.Errorf("numeric column OID = %d, want %d", oid, oidInt8)
	}
	if size := int16(binary.BigEndian.Uint16(rest[10:12])); size != 8 {
		t.Errorf("numeric column size = %d, want 8", size)
	}
}

// A row that does not match its columns is a protocol violation the client sees
// as a corrupt stream. Reporting it to the caller turns a garbled session into
// an error with a name.
func TestResultSetRefusesAMismatchedRow(t *testing.T) {
	rs := NewResultSet(TextColumn("a"), TextColumn("b"))
	rs.Row("only one")

	var buf bytes.Buffer
	err := rs.Send(&buf)
	if err == nil {
		t.Fatal("Send accepted a row with the wrong number of values")
	}
	if !strings.Contains(err.Error(), "columns") {
		t.Errorf("error does not name the cause: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes despite the error", buf.Len())
	}
}

func TestResultSetSendsAnEmptySetWithNoRows(t *testing.T) {
	rs := NewResultSet(TextColumn("nothing"))

	var buf bytes.Buffer
	if err := rs.Send(&buf); err != nil {
		t.Fatalf("Send: %v", err)
	}

	frames := decode(t, buf.Bytes())
	if got, want := len(frames), 3; got != want {
		t.Fatalf("got %d messages, want %d", got, want)
	}
	if got, want := string(frames[1].body), "SELECT 0\x00"; got != want {
		t.Errorf("CommandComplete = %q, want %q", got, want)
	}
}

func TestReadCommandBoundsTheLengthPrefix(t *testing.T) {
	// A four-byte length is four gigabytes if it is believed. The prefix comes
	// from an unauthenticated socket, so believing it is how a proxy is turned
	// into a memory-exhaustion primitive.
	oversized := []byte{'Q', 0x7F, 0xFF, 0xFF, 0xFF}
	if _, _, err := ReadCommand(bytes.NewReader(oversized)); err == nil {
		t.Fatal("ReadCommand accepted a length of 2GB")
	}

	short := []byte{'Q', 0, 0, 0, 3}
	if _, _, err := ReadCommand(bytes.NewReader(short)); err == nil {
		t.Fatal("ReadCommand accepted a length that does not cover itself")
	}
}

func TestReadCommandReturnsTheQueryText(t *testing.T) {
	query := "SHOW POOLS;"
	msg := appendTagged(nil, 'Q', append([]byte(query), 0))

	tag, body, err := ReadCommand(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	if tag != 'Q' {
		t.Errorf("tag = %q, want Q", tag)
	}
	if got := QueryText(body); got != query {
		t.Errorf("QueryText = %q, want %q", got, query)
	}
}

// A driver decodes by the format it asked for in Bind, not by what the
// RowDescription declared. pgx requests binary for every type it has a binary
// codec for, so sending an int8 column as text made it report "invalid length
// for int8" and fail the whole query.
func TestResultSetHonoursRequestedBinaryFormat(t *testing.T) {
	rs := NewResultSet(TextColumn("name"), NumericColumn("count"))
	rs.Row("orders", "42")

	var buf bytes.Buffer
	// One code per column: text for the name, binary for the number.
	if err := rs.SendRows(&buf, []int16{formatText, formatBinary}); err != nil {
		t.Fatalf("SendRows: %v", err)
	}

	row := decode(t, buf.Bytes())[0]
	if row.tag != 'D' {
		t.Fatalf("tag = %q, want D", row.tag)
	}

	rest := row.body[2:]
	size := int(int32(binary.BigEndian.Uint32(rest[:4])))
	if got := string(rest[4 : 4+size]); got != "orders" {
		t.Errorf("text column = %q, want orders", got)
	}
	rest = rest[4+size:]

	size = int(int32(binary.BigEndian.Uint32(rest[:4])))
	if size != 8 {
		t.Fatalf("binary int8 length = %d, want 8", size)
	}
	if got := binary.BigEndian.Uint64(rest[4:12]); got != 42 {
		t.Errorf("binary int8 value = %d, want 42", got)
	}
}

// The RowDescription has to declare the same format the rows are sent in, or a
// driver decodes bytes it was told to read a different way.
func TestResultSetDescribesTheRequestedFormat(t *testing.T) {
	rs := NewResultSet(TextColumn("name"), NumericColumn("count"))

	var buf bytes.Buffer
	if err := rs.Description(&buf, []int16{formatText, formatBinary}); err != nil {
		t.Fatalf("Description: %v", err)
	}

	body := decode(t, buf.Bytes())[0].body
	rest := body[2:]
	_, rest, _ = bytes.Cut(rest, []byte{0})
	if got := int16(binary.BigEndian.Uint16(rest[16:18])); got != formatText {
		t.Errorf("first column format = %d, want text", got)
	}
	rest = rest[18:]
	_, rest, _ = bytes.Cut(rest, []byte{0})
	if got := int16(binary.BigEndian.Uint16(rest[16:18])); got != formatBinary {
		t.Errorf("second column format = %d, want binary", got)
	}
}

// A single format code applies to every column, which is the shape a driver
// uses when it wants one encoding for the whole row.
func TestFormatForSpreadsASingleCode(t *testing.T) {
	if got := formatFor([]int16{formatBinary}, 3); got != formatBinary {
		t.Errorf("formatFor with one code = %d, want binary for every column", got)
	}
	if got := formatFor(nil, 0); got != formatText {
		t.Errorf("formatFor with no codes = %d, want text", got)
	}
	// Fewer codes than columns is malformed; text is the safe reading.
	if got := formatFor([]int16{formatBinary, formatBinary}, 5); got != formatText {
		t.Errorf("formatFor past the end = %d, want text", got)
	}
}

func TestDecodeBindResultFormats(t *testing.T) {
	// portal "", statement "stmt", one parameter format, one parameter of four
	// bytes, then two result formats.
	body := []byte{0}
	body = append(body, "stmt"...)
	body = append(body, 0)
	body = binary.BigEndian.AppendUint16(body, 1)
	body = binary.BigEndian.AppendUint16(body, 0)
	body = binary.BigEndian.AppendUint16(body, 1)
	body = binary.BigEndian.AppendUint32(body, 4)
	body = append(body, 1, 2, 3, 4)
	body = binary.BigEndian.AppendUint16(body, 2)
	body = binary.BigEndian.AppendUint16(body, 0)
	body = binary.BigEndian.AppendUint16(body, 1)

	formats, err := DecodeBindResultFormats(body)
	if err != nil {
		t.Fatalf("DecodeBindResultFormats: %v", err)
	}
	if len(formats) != 2 || formats[0] != formatText || formats[1] != formatBinary {
		t.Errorf("formats = %v, want [0 1]", formats)
	}

	// A NULL parameter carries a length of -1 and no bytes; walking past it as
	// though it carried four gigabytes is how this decoder would go wrong.
	null := []byte{0}
	null = append(null, 0)
	null = binary.BigEndian.AppendUint16(null, 0)
	null = binary.BigEndian.AppendUint16(null, 1)
	null = binary.BigEndian.AppendUint32(null, ^uint32(0))
	null = binary.BigEndian.AppendUint16(null, 1)
	null = binary.BigEndian.AppendUint16(null, 1)

	formats, err = DecodeBindResultFormats(null)
	if err != nil {
		t.Fatalf("DecodeBindResultFormats with a NULL parameter: %v", err)
	}
	if len(formats) != 1 || formats[0] != formatBinary {
		t.Errorf("formats = %v, want [1]", formats)
	}

	if _, err := DecodeBindResultFormats([]byte{0, 0, 0}); err == nil {
		t.Error("a truncated Bind message was accepted")
	}
}
