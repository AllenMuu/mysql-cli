// Package format renders result.Result values in json/table/csv/tsv and
// builds the JSON success/error envelopes consumed by AI agents.
package format

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/olekukonko/tablewriter"
)

// SuccessJSON renders the success envelope.
func SuccessJSON(r result.Result, meta map[string]any) string {
	env := map[string]any{
		"success": true,
		"data": map[string]any{
			"columns": r.Columns,
			"rows":    r.Rows,
		},
		"rows_affected": r.RowsAffected,
		"meta":          meta,
	}
	b, _ := json.Marshal(env)
	return string(b)
}

// ErrorJSON renders the error envelope.
func ErrorJSON(code, message string) string {
	env := map[string]any{
		"success": false,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	b, _ := json.Marshal(env)
	return string(b)
}

// Format renders r in the requested format. csv/tsv encode NULL as empty
// string; table renders NULL as "NULL"; json is handled by SuccessJSON.
func Format(r result.Result, format string) (string, error) {
	switch strings.ToLower(format) {
	case "json":
		return SuccessJSON(r, nil), nil
	case "table":
		return formatTable(r), nil
	case "csv":
		return formatDelimited(r, ",")
	case "tsv":
		return formatDelimited(r, "\t")
	default:
		return "", errors.New("unknown format: " + format)
	}
}

func cellString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func formatDelimited(r result.Result, sep string) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(r.Columns); err != nil {
		return "", err
	}
	for _, row := range r.Rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = cellString(c)
		}
		if err := w.Write(cells); err != nil {
			return "", err
		}
	}
	w.Flush()
	if sep == "\t" {
		// csv.Writer always uses comma; rewrite to tab.
		return strings.ReplaceAll(buf.String(), ",", "\t"), nil
	}
	return buf.String(), nil
}

func formatTable(r result.Result) string {
	var buf bytes.Buffer
	tw := tablewriter.NewWriter(&buf)
	tw.SetHeader(r.Columns)
	for _, row := range r.Rows {
		cells := make([]string, len(row))
		for i, c := range row {
			if c == nil {
				cells[i] = "NULL"
			} else {
				cells[i] = fmt.Sprintf("%v", c)
			}
		}
		tw.Append(cells)
	}
	tw.Render()
	return buf.String()
}
