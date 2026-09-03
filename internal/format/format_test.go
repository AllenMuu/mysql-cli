package format

import (
	"encoding/json"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/stretchr/testify/assert"
)

func TestCSV(t *testing.T) {
	r := result.Result{Columns: []string{"id", "name"}, Rows: [][]any{{1, "a"}, {nil, "b"}}}
	out, err := Format(r, "csv")
	assert.NoError(t, err)
	assert.Equal(t, "id,name\n1,a\n,b\n", out)
}

func TestTSV(t *testing.T) {
	r := result.Result{Columns: []string{"id", "name"}, Rows: [][]any{{1, "a"}}}
	out, err := Format(r, "tsv")
	assert.NoError(t, err)
	assert.Equal(t, "id\tname\n1\ta\n", out)
}

func TestTableNull(t *testing.T) {
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{nil}}}
	out, err := Format(r, "table")
	assert.NoError(t, err)
	assert.Contains(t, out, "NULL")
}

func TestJSONEnvelope(t *testing.T) {
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{1}}, RowsAffected: 1}
	out := SuccessJSON(r, map[string]any{"datasource": "dev", "elapsed_ms": 12})
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Columns []string `json:"columns"`
			Rows    [][]any  `json:"rows"`
		} `json:"data"`
		RowsAffected int            `json:"rows_affected"`
		Meta         map[string]any `json:"meta"`
	}
	assert.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.True(t, env.Success)
	assert.Equal(t, []string{"id"}, env.Data.Columns)
	assert.Equal(t, float64(12), env.Meta["elapsed_ms"])
}

func TestJSONNullIsLiteral(t *testing.T) {
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{nil}}}
	out := SuccessJSON(r, nil)
	assert.Contains(t, out, "null")
}

func TestErrorJSON(t *testing.T) {
	out := ErrorJSON("READONLY_VIOLATION", "UPDATE requires --write")
	assert.Contains(t, out, `"code":"READONLY_VIOLATION"`)
	assert.Contains(t, out, `"success":false`)
}

func TestUnknownFormatErrors(t *testing.T) {
	_, err := Format(result.Empty(), "xml")
	assert.Error(t, err)
}

func TestCSVCommaEscaping(t *testing.T) {
	r := result.Result{Columns: []string{"s"}, Rows: [][]any{{"a,b"}}}
	out, err := Format(r, "csv")
	assert.NoError(t, err)
	assert.Contains(t, out, `"a,b"`)
}

func TestTSVCommaInValue(t *testing.T) {
	r := result.Result{Columns: []string{"s"}, Rows: [][]any{{"a,b"}}}
	out, err := Format(r, "tsv")
	assert.NoError(t, err)
	assert.Contains(t, out, "a,b")
	assert.NotContains(t, out, "a\tb")
}

func TestReadJSONOmitsRowsAffectedAndAddsMeta(t *testing.T) {
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	out := ReadJSON(r, 1000)
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Rows [][]any `json:"rows"`
		} `json:"data"`
		RowsAffected *int           `json:"rows_affected"` // pointer: nil when absent
		Meta         map[string]any `json:"meta"`
	}
	assert.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.True(t, env.Success)
	assert.Nil(t, env.RowsAffected) // SELECT omits rows_affected
	assert.Equal(t, true, env.Meta["truncated"])
	assert.Equal(t, float64(1000), env.Meta["limit"])
}

func TestJSONL(t *testing.T) {
	r := result.Result{Columns: []string{"id", "name"}, Rows: [][]any{{1, "a"}, {nil, "b"}}}
	out, err := Format(r, "jsonl")
	assert.NoError(t, err)
	assert.Equal(t, `{"id":1,"name":"a"}`+"\n"+`{"id":null,"name":"b"}`+"\n", out)
}

// TestJSONLDuplicateColumnNames 验证 A7：JOIN 同名列（SELECT u.id, o.id）
// 组装 map 时后者不能静默覆盖前者，重复列名从第二个开始加 _2 后缀。
func TestJSONLDuplicateColumnNames(t *testing.T) {
	r := result.Result{Columns: []string{"id", "name", "id"}, Rows: [][]any{{1, "a", 2}}}
	out, err := Format(r, "jsonl")
	assert.NoError(t, err)
	assert.Equal(t, `{"id":1,"id_2":2,"name":"a"}`+"\n", out)
}

// TestJSONLTripleDuplicateColumnNames 三重复：id / id_2 / id_3。
func TestJSONLTripleDuplicateColumnNames(t *testing.T) {
	r := result.Result{Columns: []string{"id", "id", "id"}, Rows: [][]any{{1, 2, 3}}}
	out, err := Format(r, "jsonl")
	assert.NoError(t, err)
	assert.Equal(t, `{"id":1,"id_2":2,"id_3":3}`+"\n", out)
}

// TestJSONLDuplicateSkipsExistingName 生成后缀时跳过已被真实列名占用的
// 候选名：列 [id, id_2, id] 的第三个 id 应得到 id_3 而不是与真列撞车。
func TestJSONLDuplicateSkipsExistingName(t *testing.T) {
	r := result.Result{Columns: []string{"id", "id_2", "id"}, Rows: [][]any{{1, 2, 3}}}
	out, err := Format(r, "jsonl")
	assert.NoError(t, err)
	assert.Equal(t, `{"id":1,"id_2":2,"id_3":3}`+"\n", out)
}
