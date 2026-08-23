package output

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Column is one table column: the JSON field it reads and how to render it.
type Column struct {
	Header string
	Field  string
	// Format renders one cell. Nil prints the value as-is.
	Format func(any) string
}

// JSON writes the raw API response through, pretty-printed. Everything the CLI
// does not model is still reachable this way, which is why no command hides a
// field it happens not to have a column for.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table writes aligned columns. Empty input prints a single explanatory line
// rather than a bare header: a table with nothing under it reads as a failure
// when it usually means the window was quiet.
func Table(w io.Writer, cols []Column, rows []map[string]any, empty string) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, empty)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.Header
	}
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			v, ok := row[c.Field]
			switch {
			case !ok || v == nil:
				cells[i] = "—"
			case c.Format != nil:
				cells[i] = c.Format(v)
			default:
				cells[i] = fmt.Sprint(v)
			}
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// Num renders a number with a fixed number of decimals, dropping the float
// noise that makes a column impossible to scan.
func Num(decimals int) func(any) string {
	return func(v any) string {
		f, ok := toFloat(v)
		if !ok {
			return fmt.Sprint(v)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "—"
		}
		return strconv.FormatFloat(f, 'f', decimals, 64)
	}
}

// Percent renders a 0..1 ratio as a percentage, which is how error rates are
// discussed even though the API sends the ratio.
func Percent(v any) string {
	f, ok := toFloat(v)
	if !ok {
		return fmt.Sprint(v)
	}
	return strconv.FormatFloat(f*100, 'f', 2, 64) + "%"
}
