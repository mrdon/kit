package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// pgsshClient runs SQL against a remote Dokku-managed postgres by piping
// into `ssh <host> 'postgres:connect <service>'` (psql under the hood).
// This avoids needing postgres:expose — the migration tool reuses the
// same SSH+dokku channel we use for ad-hoc validation queries.
//
// All inputs are formatted into SQL strings here rather than parameterized
// because psql in interactive mode doesn't speak the protocol-level bind
// path. The migration tool only feeds inputs that are either UUIDs, hex
// blobs (via decode()), JSON literals, or integers — none of which need
// runtime escaping beyond what hex/json encoding already gives. For any
// raw text inputs we'd add real escaping; we don't have any.
type pgsshClient struct {
	sshHost   string // e.g. "dokku@apps.twdata.org"
	pgService string // e.g. "kit-db"
}

func newPgsshClient(sshHost, pgService string) *pgsshClient {
	return &pgsshClient{sshHost: sshHost, pgService: pgService}
}

// queryCSV runs a single SELECT via COPY ... TO STDOUT WITH (FORMAT csv),
// parses the result, and returns the parsed rows. The outer query is
// wrapped in COPY automatically by the caller's selectExpr / fromExpr.
//
// Returns rows as [][]string. Each row's column count matches the SELECT.
func (c *pgsshClient) queryCSV(selectSQL string) ([][]string, error) {
	script := "\\set ON_ERROR_STOP on\nCOPY (" + selectSQL + ") TO STDOUT WITH (FORMAT csv);\n"
	stdout, stderr, err := c.execPsql(script)
	if err != nil {
		return nil, fmt.Errorf("psql: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	if stdout == "" {
		return nil, nil
	}
	rdr := csv.NewReader(strings.NewReader(stdout))
	rdr.FieldsPerRecord = -1
	rows, err := rdr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing csv: %w", err)
	}
	return rows, nil
}

// exec runs arbitrary SQL with no expected rows (INSERT/UPDATE/etc.).
// Errors from psql come back on stderr with a non-zero exit code; we
// surface both.
func (c *pgsshClient) exec(sqlScript string) error {
	script := "\\set ON_ERROR_STOP on\n" + sqlScript + "\n"
	_, stderr, err := c.execPsql(script)
	if err != nil {
		return fmt.Errorf("psql: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	return nil
}

// execPsql runs the ssh+psql subprocess with the given SQL on stdin.
func (c *pgsshClient) execPsql(sqlScript string) (stdout, stderr string, err error) {
	cmd := exec.Command("ssh", c.sshHost, "postgres:connect "+c.pgService)
	cmd.Stdin = strings.NewReader(sqlScript)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return stdout, stderr, fmt.Errorf("exit %d", exitErr.ExitCode())
		}
		return stdout, stderr, runErr
	}
	return stdout, stderr, nil
}

// hexLiteral renders a byte slice as a SQL `decode('<hex>', 'hex')` expr
// suitable for use in INSERT VALUES or WHERE clauses.
func hexLiteral(b []byte) string {
	return fmt.Sprintf("decode('%x', 'hex')", b)
}

// jsonbLiteral renders bytes as a SQL '<json>'::jsonb literal. The bytes
// must already be valid JSON (we produce them with json.Marshal). The
// only character we need to escape is single-quote (')— there are none
// in our generated kdf_params payloads (algo/iterations/salt-base64) so
// the trivial replacer is sufficient. If we ever feed user-controlled
// strings into JSON here, swap this for SQL-side server-binding instead.
func jsonbLiteral(b []byte) string {
	s := string(b)
	if strings.ContainsRune(s, '\'') {
		// Defensive: should never trigger for our generated content.
		s = strings.ReplaceAll(s, "'", "''")
	}
	return "'" + s + "'::jsonb"
}

// uuidLiteral renders a uuid as a SQL quoted literal.
func uuidLiteral(s string) string {
	// uuid.UUID String() is already valid hex+hyphens; no escaping needed.
	return "'" + s + "'"
}

// stderrPrintf is a tiny helper for status output to keep stdout clean
// in case the user pipes it.
func stderrPrintf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

// hexDecode is a thin wrapper that returns a friendlier error on bad input.
func hexDecode(s string) ([]byte, error) {
	b, err := hexDecodeBytes([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("bad hex input %q: %w", s, err)
	}
	return b, nil
}

// hexDecodeBytes mirrors encoding/hex.DecodeString but takes a byte
// slice to avoid an extra copy when reading CSV cells.
func hexDecodeBytes(src []byte) ([]byte, error) {
	if len(src)%2 != 0 {
		return nil, errors.New("odd-length hex")
	}
	out := make([]byte, len(src)/2)
	for i := range out {
		hi, ok1 := hexNibble(src[i*2])
		lo, ok2 := hexNibble(src[i*2+1])
		if !ok1 || !ok2 {
			return nil, errors.New("bad hex digit")
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
