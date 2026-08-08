package protocol

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"

	"github.com/gsoultan/pontus/pkg/buffer"
)

// SlotStat is one row of pg_replication_slots, with the retained WAL computed
// against the current write position.
type SlotStat struct {
	Name         string
	Plugin       string
	SlotType     string
	Active       bool
	Database     string
	ConfirmedLSN string
	// RetainedBytes is how much WAL the slot is holding, which is the number
	// that decides whether an abandoned consumer fills the disk.
	RetainedBytes int64
}

// slotStatsQuery reads every slot with the WAL each is holding.
//
// Reported per slot rather than per stream on purpose: PostgreSQL sees the
// proxy's address in pg_stat_replication, not the consumer's, so a proxied
// stream cannot be correlated to a slot without parsing the replication
// command exchange. Inventing that join would produce confident, wrong
// attribution; the slots are authoritative on their own.
const slotStatsQuery = `SELECT slot_name, coalesce(plugin,''), slot_type, active, ` +
	`coalesce(database,''), coalesce(confirmed_flush_lsn::text,''), ` +
	`coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)::bigint, 0) ` +
	`FROM pg_replication_slots`

// QuerySlotStats runs the slot inventory on an already-authenticated
// connection.
func (p *PostgresHandler) QuerySlotStats(ctx context.Context, conn net.Conn) ([]SlotStat, error) {
	rows, err := p.querySimple(ctx, conn, slotStatsQuery, 7)
	if err != nil {
		return nil, err
	}

	out := make([]SlotStat, 0, len(rows))
	for _, row := range rows {
		retained, _ := strconv.ParseInt(row[6], 10, 64)
		out = append(out, SlotStat{
			Name:          row[0],
			Plugin:        row[1],
			SlotType:      row[2],
			Active:        row[3] == "t" || row[3] == "true",
			Database:      row[4],
			ConfirmedLSN:  row[5],
			RetainedBytes: retained,
		})
	}
	return out, nil
}

// querySimple runs a simple query and returns its DataRows as text columns.
//
// The existing readers parse a single scalar inline and stop at the first
// value; a multi-row, multi-column result needs the message framing followed
// properly, which is what this does.
func (p *PostgresHandler) querySimple(ctx context.Context, conn net.Conn, query string, cols int) ([][]string, error) {
	payload := make([]byte, 0, 6+len(query))
	payload = append(payload, 'Q')
	payload = binary.BigEndian.AppendUint32(payload, uint32(4+len(query)+1))
	payload = append(payload, query...)
	payload = append(payload, 0)

	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("send query: %w", err)
	}

	buf := buffer.Get()
	defer buffer.Put(buf)

	var (
		pending []byte
		rows    [][]string
		errMsg  string
	)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		n, err := conn.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
		}
		if err != nil {
			return nil, err
		}

		for len(pending) >= 5 {
			msgLen := int(binary.BigEndian.Uint32(pending[1:5]))
			if msgLen < 4 || len(pending) < msgLen+1 {
				break // wait for the rest of this message
			}

			msgType := pending[0]
			body := pending[5 : msgLen+1]
			pending = pending[msgLen+1:]

			switch msgType {
			case 'D':
				if row, ok := parseDataRow(body, cols); ok {
					rows = append(rows, row)
				}
			case 'E':
				errMsg = errorMessageText(body)
			case 'Z':
				if errMsg != "" {
					return nil, fmt.Errorf("query failed: %s", errMsg)
				}
				return rows, nil
			}
		}
	}
}

// parseDataRow decodes a DataRow body into text columns. A NULL column arrives
// as length -1 and becomes an empty string.
func parseDataRow(body []byte, cols int) ([]string, bool) {
	if len(body) < 2 {
		return nil, false
	}
	count := int(binary.BigEndian.Uint16(body[:2]))
	if count < cols {
		return nil, false
	}

	out := make([]string, 0, count)
	offset := 2
	for range count {
		if offset+4 > len(body) {
			return nil, false
		}
		size := int(int32(binary.BigEndian.Uint32(body[offset : offset+4])))
		offset += 4
		if size < 0 {
			out = append(out, "")
			continue
		}
		if offset+size > len(body) {
			return nil, false
		}
		out = append(out, string(body[offset:offset+size]))
		offset += size
	}
	return out, true
}

// errorMessageText pulls the human-readable field out of an ErrorResponse.
func errorMessageText(body []byte) string {
	for i := 0; i < len(body); {
		tag := body[i]
		if tag == 0 {
			break
		}
		i++
		end := i
		for end < len(body) && body[end] != 0 {
			end++
		}
		if tag == 'M' {
			return string(body[i:end])
		}
		i = end + 1
	}
	return "unknown error"
}
