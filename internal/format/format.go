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
	if meta == nil {
		meta = map[string]any{}
	}
	env := map[string]any{
		"success": true,
		"data": map[string]any{
			"columns": r.Columns,
			"rows":    r.Rows,
		},
		"rows_affected": r.RowsAffected,
		"meta":          meta,
	}
	b, err := json.Marshal(env)
	if err != nil {
		return ErrorJSON("FORMAT_ERROR", "json marshal failed: "+err.Error())
	}
	return string(b)
}

// ReadJSON renders the success envelope for a read query: omits rows_affected
// (always 0 for SELECT) and reports truncated/limit in meta.
func ReadJSON(r result.Result, limit int) string {
	env := map[string]any{
		"success": true,
		"data": map[string]any{
			"columns": r.Columns,
			"rows":    r.Rows,
		},
		"meta": map[string]any{
			"truncated": r.Truncated,
			"limit":     limit,
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		return ErrorJSON("FORMAT_ERROR", "json marshal failed: "+err.Error())
	}
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
	b, err := json.Marshal(env)
	if err != nil {
		return `{"success":false,"error":{"code":"FORMAT_ERROR","message":"marshal failed"}}`
	}
	return string(b)
}

// Format renders r in the requested format. csv/tsv encode NULL as empty
// string; table renders NULL as "NULL"; jsonl renders each row as a JSON
// object with NULL as native null; json is handled by SuccessJSON.
func Format(r result.Result, format string) (string, error) {
	switch strings.ToLower(format) {
	case "json":
		return SuccessJSON(r, nil), nil
	case "jsonl":
		return formatJSONL(r)
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

// cellString 把单元格值转成字符串供 csv/tsv/table 使用。nil 与空字符串都返回 ""。
// 注意：table 格式需要在 cellString 之外区分 nil 与 ""（渲染为 NULL vs 空单元格），
// 用 cellIsNil 判断；csv/tsv 两者都渲染为空字段。
func cellString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// cellIsNil 报告单元格是否为 nil（区分 NULL 与空字符串）。
func cellIsNil(v any) bool { return v == nil }

func formatDelimited(r result.Result, sep string) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Comma = []rune(sep)[0]
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
	return buf.String(), nil
}

func formatTable(r result.Result) string {
	var buf bytes.Buffer
	tw := tablewriter.NewWriter(&buf)
	tw.SetHeader(r.Columns)
	for _, row := range r.Rows {
		cells := make([]string, len(row))
		for i, c := range row {
			// 只把 nil 渲染为 "NULL"，空字符串保留为空单元格——
			// 与数据库 NULL vs '' 语义对齐，避免以前 nil/"" 都渲染成 "NULL" 的失真。
			if cellIsNil(c) {
				cells[i] = "NULL"
			} else {
				cells[i] = cellString(c)
			}
		}
		tw.Append(cells)
	}
	tw.Render()
	return buf.String()
}

func formatJSONL(r result.Result) (string, error) {
	var buf bytes.Buffer
	for _, row := range r.Rows {
		obj := make(map[string]any, len(row))
		for i, c := range row {
			if i < len(r.Columns) {
				obj[r.Columns[i]] = c
			}
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return "", err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.String(), nil
}
