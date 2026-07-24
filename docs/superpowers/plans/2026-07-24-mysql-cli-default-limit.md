# mysql-cli 默认安全 LIMIT + 信封瘦身 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 mysql-cli 的 SELECT 加默认安全行数 cap(默认 1000,cap+1 探测截断,`meta.truncated` 标记),并瘦身 JSON 信封(SELECT 省 `rows_affected` + 新增 `--format jsonl`),消灭裸跑全表扫的 token 灾难。

**Architecture:** 默认 cap 在 query 层实现:`applyLimit` 在 probe 模式 wrap `LIMIT cap+1`,`Execute` 扫描后按行数判定截断并设 `result.Result.Truncated`。truncated 经 Result 传到 cli,cli 按 read/write 分流到 `format.ReadJSON`(省 rows_affected + `meta{truncated,limit}`)或 `SuccessJSON`。开关:`--no-limit` flag、config `default_limit`、env `MYSQL_CLI_DEFAULT_LIMIT`,优先级 `--limit` > `--no-limit` > config > env > 1000。

**Tech Stack:** Go 1.22+, spf13/cobra, DATA-DOG/go-sqlmock, stretchr/testify, BurntSushi/toml, olekukonko/tablewriter

## Global Constraints

- Go 1.22+;`go build ./...`、`go vet ./...`、`go test ./...` 必须通过
- 退出码契约不变(truncated 在 meta,不新增退出码;`Exit*` 常量不动)
- `internal/result` 保持 dependency-free(不加 meta map;`Truncated bool` 字段可接受)
- `applyLimit`/`hasLimit`/`selectRe` 未导出,测试在 `query` 包内可直接调
- skill 改完 `go build` 重新 embed(bundle);`./scripts/skill-format-check.sh skills/` 必须过
- conventional commits;每个 task 末尾 commit
- 集成测试 testcontainers 需 `RUN_INTEGRATION=1`,默认跳过
- breaking change:SELECT 默认 cap 1000 + SELECT json 省 `rows_affected`,CHANGELOG 明示

## File Structure

| 文件 | 责任 | 改动 |
|---|---|---|
| `internal/result/result.go` | 无依赖结果契约 | 加 `Truncated bool` 字段 |
| `internal/query/query.go` | 读查询执行 + LIMIT wrap | `Options.Probe`;`applyLimit(sql,limit,probe)`;Execute 截断 |
| `internal/query/query_test.go` | query 单测(sqlmock) | probe 截断两分支测试 |
| `internal/config/config.go` | TOML/env 配置 | `Config.DefaultLimit` + `fileConfig` + `LoadFile` 映射 |
| `internal/config/config_test.go` | config 单测 | `default_limit` 解析测试 |
| `internal/format/format.go` | 输出渲染 | `ReadJSON(r,limit)` + `jsonl` 分支 |
| `internal/format/format_test.go` | format 单测 | ReadJSON/jsonl 测试 |
| `internal/cli/root.go` | cobra 装配 + 全局 flag | `--no-limit` flag;`Globals.NoLimit/DefaultLimit/eout`;jsonl 校验 |
| `internal/cli/commands.go` | 子命令 + emit | `resolveCap`/`defaultCap`/`emitReadResult`;newQueryCmd read 分流 |
| `internal/cli/commands_test.go` | cli 单测 | cap 优先级 + emit 测试 |
| `skills/mysql-shared/SKILL.md` | 共享规则 | 默认 cap + truncated + --no-limit + jsonl;version 1.1.0 |
| `skills/mysql-query/SKILL.md` | query 技能 | agent 适配指引;version 1.1.0 |
| `CHANGELOG.md` | 变更记录 | breaking 条目 |

---

### Task 1: result.Result 加 Truncated 字段

**Files:**
- Modify: `internal/result/result.go:9-14`
- Test: `internal/result/result_test.go`

**Interfaces:**
- Produces: `result.Result.Truncated bool`(后续 query/format/cli 任务依赖)

- [ ] **Step 1: Write the failing test**

追加到 `internal/result/result_test.go` 末尾:

```go
func TestTruncatedField(t *testing.T) {
	r := Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	assert.True(t, r.Truncated)

	zero := Result{}
	assert.False(t, zero.Truncated) // 零值为 false
}
```

若 `result_test.go` 顶部没有 `import "github.com/stretchr/testify/assert"`,补上(参考现有测试)。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/result/ -run TestTruncatedField -v`
Expected: FAIL / 编译错误 `unknown field 'Truncated' in struct literal of type Result`

- [ ] **Step 3: Write minimal implementation**

修改 `internal/result/result.go` 的 `Result` 结构体(原 9-14 行):

```go
type Result struct {
	Columns      []string
	Rows         [][]any
	RowsAffected int64
	LastInsertID int64
	Truncated    bool
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/result/ -v`
Expected: PASS(含新测试 + 现有测试)

- [ ] **Step 5: Commit**

```bash
git add internal/result/result.go internal/result/result_test.go
git commit -m "feat(result): add Truncated field to Result"
```

---

### Task 2: query 层默认 cap + cap+1 探测截断

**Files:**
- Modify: `internal/query/query.go:19-25`(Options)、`query.go:48`(Execute 调 applyLimit)、`query.go:92-105`(applyLimit)、`query.go:69-86`(Execute 扫描循环)
- Test: `internal/query/query_test.go`

**Interfaces:**
- Consumes: `result.Result.Truncated`(Task 1)
- Produces: `query.Options.Probe bool`;`Execute` 在 probe 模式设 `r.Truncated` 并截断行;`applyLimit(sqlText, limit, probe)` 签名

- [ ] **Step 1: Write the failing tests**

追加到 `internal/query/query_test.go` 末尾:

```go
func TestApplyLimitProbe(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		limit    int
		probe    bool
		expected string
	}{
		{name: "probe wraps limit+1", sql: "SELECT id FROM t", limit: 100, probe: true, expected: "SELECT * FROM (SELECT id FROM t) AS _q LIMIT 101"},
		{name: "no probe wraps limit", sql: "SELECT id FROM t", limit: 100, probe: false, expected: "SELECT * FROM (SELECT id FROM t) AS _q LIMIT 100"},
		{name: "probe ignored when limit<=0", sql: "SELECT id FROM t", limit: 0, probe: true, expected: "SELECT id FROM t"},
		{name: "probe ignored when hasLimit", sql: "SELECT id FROM t LIMIT 5", limit: 100, probe: true, expected: "SELECT id FROM t LIMIT 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, applyLimit(tt.sql, tt.limit, tt.probe))
		})
	}
}

func TestExecuteProbeTruncates(t *testing.T) {
	pool, mock := newMock(t)
	// probe limit=2 -> wraps LIMIT 3, 返回 3 行 -> truncated, 保留 2 行
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3)
	mock.ExpectQuery("SELECT \\* FROM \\(SELECT id FROM t\\) AS _q LIMIT 3").WillReturnRows(rows)
	r, err := Execute(context.Background(), pool, "SELECT id FROM t", Options{Limit: 2, Probe: true})
	assert.NoError(t, err)
	assert.True(t, r.Truncated)
	assert.Equal(t, 2, len(r.Rows))
}

func TestExecuteProbeNoTruncate(t *testing.T) {
	pool, mock := newMock(t)
	// probe limit=2 -> wraps LIMIT 3, 返回 2 行(<=limit) -> 未截断
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2)
	mock.ExpectQuery("SELECT \\* FROM \\(SELECT id FROM t\\) AS _q LIMIT 3").WillReturnRows(rows)
	r, err := Execute(context.Background(), pool, "SELECT id FROM t", Options{Limit: 2, Probe: true})
	assert.NoError(t, err)
	assert.False(t, r.Truncated)
	assert.Equal(t, 2, len(r.Rows))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/query/ -run 'TestApplyLimitProbe|TestExecuteProbe' -v`
Expected: FAIL / 编译错误(`applyLimit` 签名不匹配;`Options` 无 `Probe`)

- [ ] **Step 3: Update applyLimit signature + Options**

`internal/query/query.go` Options(原 19-25 行):

```go
type Options struct {
	Write   bool
	DDL     bool
	Yes     bool
	Limit   int
	Probe   bool
	Timeout time.Duration
}
```

`applyLimit`(原 92-105 行):

```go
// applyLimit wraps a SELECT with an outer LIMIT when one is requested and
// the statement is a read query without its own LIMIT. In probe mode it
// requests limit+1 rows so the caller can detect truncation.
func applyLimit(sqlText string, limit int, probe bool) string {
	if limit <= 0 || !selectRe.MatchString(sqlText) {
		return sqlText
	}
	if hasLimit(sqlText) {
		return sqlText
	}
	cleaned := strings.TrimRight(strings.TrimSpace(sqlText), ";")
	n := limit
	if probe {
		n = limit + 1
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS _q LIMIT %d", cleaned, n)
}
```

- [ ] **Step 4: Update Execute to pass probe + truncate**

`internal/query/query.go` Execute 中(原 48 行)把:

```go
	execSQL := applyLimit(sqlText, opts.Limit)
```

改为:

```go
	execSQL := applyLimit(sqlText, opts.Limit, opts.Probe)
```

在 Execute 的 `return res, nil` 之前(原 89 行前,`rows.Err()` 检查之后)插入截断逻辑:

```go
	if opts.Probe && opts.Limit > 0 && len(res.Rows) > opts.Limit {
		res.Truncated = true
		res.Rows = res.Rows[:opts.Limit]
	}
	return res, nil
```

- [ ] **Step 5: Update existing TestApplyLimit call sites**

`internal/query/query_test.go` 的 `TestApplyLimit`(原 67-111)所有 `applyLimit(tt.sql, tt.limit)` 调用改为 `applyLimit(tt.sql, tt.limit, false)`:

```go
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, applyLimit(tt.sql, tt.limit, false))
		})
```

`TestApplyLimitIgnoresLimitInStringLiteral`(原 113-119)的 `applyLimit(sql, 100)` 改为 `applyLimit(sql, 100, false)`。

- [ ] **Step 6: Run all query tests**

Run: `go test ./internal/query/ -v`
Expected: PASS(新测试 + 现有 TestApplyLimit/TestExecuteLimitWrapsQuery 等全过)

- [ ] **Step 7: Commit**

```bash
git add internal/query/query.go internal/query/query_test.go
git commit -m "feat(query): default safe cap with cap+1 probe + Truncated"
```

---

### Task 3: config 加 DefaultLimit

**Files:**
- Modify: `internal/config/config.go:47-50`(Config)、`config.go:52-82`(fileConfig)、`config.go:86-97`(LoadFile)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config.DefaultLimit int`(toml `default_limit`);cli Task 5 读取

- [ ] **Step 1: Write the failing test**

追加到 `internal/config/config_test.go` 末尾(参考现有 LoadFile 测试的 toml 字符串构造方式):

```go
func TestDefaultLimitFromConfig(t *testing.T) {
	toml := `
default = "dev"
default_limit = 2500

[datasource.dev]
host = "127.0.0.1"
port = 3306
`
	tmp := t.TempDir() + "/config.toml"
	assert.NoError(t, os.WriteFile(tmp, []byte(toml), 0644))
	cfg, err := LoadFile(tmp)
	assert.NoError(t, err)
	assert.Equal(t, 2500, cfg.DefaultLimit)
}

func TestDefaultLimitZeroWhenUnset(t *testing.T) {
	toml := `
default = "dev"
[datasource.dev]
host = "127.0.0.1"
`
	tmp := t.TempDir() + "/config.toml"
	assert.NoError(t, os.WriteFile(tmp, []byte(toml), 0644))
	cfg, err := LoadFile(tmp)
	assert.NoError(t, err)
	assert.Equal(t, 0, cfg.DefaultLimit)
}
```

确保 `config_test.go` 已 import `os`(若没有则补)。

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestDefaultLimit -v`
Expected: FAIL(`cfg.DefaultLimit` undefined)

- [ ] **Step 3: Add DefaultLimit to Config + fileConfig + LoadFile**

`internal/config/config.go` Config(原 47-50 行):

```go
type Config struct {
	Datasources       map[string]Datasource `toml:"datasource"`
	DefaultDatasource string                `toml:"default"`
	DefaultLimit      int                   `toml:"default_limit"`
}
```

`fileConfig` 结构(原 52-82 行,找到 `type fileConfig struct` 那个,在 `Default` 字段后加 `DefaultLimit`):

```go
type fileConfig struct {
	Default      string                    `toml:"default"`
	DefaultLimit int                       `toml:"default_limit"`
	Datasources  map[string]fileDatasource `toml:"datasource"`
}
```

(保留 fileConfig 原有其他字段,只插入 `DefaultLimit`。)

`LoadFile`(原 86-97 行)的 cfg 构造改为:

```go
	cfg := &Config{DefaultDatasource: fc.Default, DefaultLimit: fc.DefaultLimit, Datasources: map[string]Datasource{}}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS(新测试 + 现有 config 测试全过)

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add default_limit config field"
```

---

### Task 4: format.ReadJSON + jsonl 格式

**Files:**
- Modify: `internal/format/format.go`(新增 ReadJSON、jsonl 分支、formatJSONL)
- Test: `internal/format/format_test.go`

**Interfaces:**
- Consumes: `result.Result.Truncated`(Task 1)
- Produces: `format.ReadJSON(r result.Result, limit int) string`;`Format(r, "jsonl")` 分支。cli Task 5 调用

- [ ] **Step 1: Write the failing tests**

追加到 `internal/format/format_test.go` 末尾:

```go
func TestReadJSONOmitsRowsAffectedAndAddsMeta(t *testing.T) {
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	out := ReadJSON(r, 1000)
	var env struct {
		Success      bool           `json:"success"`
		Data         struct{ Rows [][]any `json:"rows"` } `json:"data"`
		RowsAffected *int           `json:"rows_affected"` // 指针:缺省时为 nil
		Meta         map[string]any `json:"meta"`
	}
	assert.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.True(t, env.Success)
	assert.Nil(t, env.RowsAffected) // SELECT 省略
	assert.Equal(t, true, env.Meta["truncated"])
	assert.Equal(t, float64(1000), env.Meta["limit"])
}

func TestJSONL(t *testing.T) {
	r := result.Result{Columns: []string{"id", "name"}, Rows: [][]any{{1, "a"}, {nil, "b"}}}
	out, err := Format(r, "jsonl")
	assert.NoError(t, err)
	assert.Equal(t, `{"id":1,"name":"a"}`+"\n"+`{"id":null,"name":"b"}`+"\n", out)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/format/ -run 'TestReadJSON|TestJSONL' -v`
Expected: FAIL(`ReadJSON` undefined;`Format(r,"jsonl")` error "unknown format")

- [ ] **Step 3: Add ReadJSON + jsonl**

`internal/format/format.go` 在 `SuccessJSON` 函数之后新增:

```go
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
```

`Format` switch(原 56-69 行)在 `case "json":` 之后、`default` 之前加:

```go
	case "jsonl":
		return formatJSONL(r), nil
```

文件末尾新增:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/format/ -v`
Expected: PASS(新测试 + 现有 TestJSONEnvelope/TestCSV 等全过;`SuccessJSON` 未改,行为不变)

- [ ] **Step 5: Commit**

```bash
git add internal/format/format.go internal/format/format_test.go
git commit -m "feat(format): ReadJSON envelope + jsonl format"
```

---

### Task 5: cli 集成 --no-limit + cap 优先级 + read 信封分流

**Files:**
- Modify: `internal/cli/root.go:32-47`(Globals)、`root.go:51`(Run 初始化)、`root.go:70-78`(PreRunE 校验)、`root.go:80-93`(flag 注册)
- Modify: `internal/cli/commands.go:27-43`(resolve 填 DefaultLimit)、`commands.go:53-65`(opts/emitResult)、`commands.go:92-104`(newQueryCmd read 分流)
- Test: `internal/cli/commands_test.go`

**Interfaces:**
- Consumes: `Options.Probe`(Task 2)、`Config.DefaultLimit`(Task 3)、`format.ReadJSON`/`Format(r,"jsonl")`(Task 4)
- Produces:`--no-limit` flag;`--format jsonl`;默认 cap 行为

- [ ] **Step 1: Write the failing tests**

追加到 `internal/cli/commands_test.go` 末尾(确保 import `bytes`、`result`、`assert`、`cobra`):

```go
func TestDefaultCapFallbackTo1000(t *testing.T) {
	g := &Globals{}
	assert.Equal(t, 1000, g.defaultCap())
}

func TestDefaultCapFromConfig(t *testing.T) {
	g := &Globals{DefaultLimit: 500}
	assert.Equal(t, 500, g.defaultCap())
}

func TestDefaultCapFromEnv(t *testing.T) {
	t.Setenv("MYSQL_CLI_DEFAULT_LIMIT", "200")
	g := &Globals{}
	assert.Equal(t, 200, g.defaultCap())
}

func TestResolveCapDefaultProbe(t *testing.T) {
	g := &Globals{DefaultLimit: 500}
	cmd := &cobra.Command{}
	limit, probe := g.resolveCap(cmd)
	assert.Equal(t, 500, limit)
	assert.True(t, probe)
}

func TestResolveCapNoLimitFlag(t *testing.T) {
	g := &Globals{NoLimit: true}
	cmd := &cobra.Command{}
	limit, probe := g.resolveCap(cmd)
	assert.Equal(t, 0, limit)
	assert.False(t, probe)
}

func TestResolveCapExplicitLimit(t *testing.T) {
	g := &Globals{Limit: 50}
	cmd := &cobra.Command{}
	cmd.Flags().Int("limit", 0, "")
	cmd.Flags().Set("limit", "50")
	limit, probe := g.resolveCap(cmd)
	assert.Equal(t, 50, limit)
	assert.False(t, probe)
}

func TestEmitReadJSONOmitsRowsAffected(t *testing.T) {
	var out bytes.Buffer
	g := &Globals{Format: "json", out: &out}
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	g.emitReadResult(r, nil, 1000)
	assert.Contains(t, out.String(), `"truncated":true`)
	assert.NotContains(t, out.String(), "rows_affected")
}

func TestEmitReadJSONLTruncatedStderr(t *testing.T) {
	var out, eout bytes.Buffer
	g := &Globals{Format: "jsonl", out: &out, eout: &eout}
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	g.emitReadResult(r, nil, 1000)
	assert.Contains(t, out.String(), `{"id":1}`)
	assert.Contains(t, eout.String(), "# truncated:true limit:1000")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestDefaultCap|TestResolveCap|TestEmitRead' -v`
Expected: FAIL(`Globals` 无 `NoLimit/DefaultLimit/eout`;`defaultCap/resolveCap/emitReadResult` undefined)

- [ ] **Step 3: Extend Globals + register --no-limit + jsonl validation**

`internal/cli/root.go` Globals(原 32-47 行)加三个字段:

```go
type Globals struct {
	Datasource   string
	Format       string
	Write        bool
	DDL          bool
	Yes          bool
	Limit        int
	NoLimit      bool
	DefaultLimit int
	Timeout      string
	ConfigPath   string
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	out          io.Writer
	eout         io.Writer
}
```

`Run`(原 51 行)初始化 eout:

```go
	g := &Globals{Format: "json", out: os.Stdout, eout: os.Stderr}
```

`PersistentPreRunE`(原 70-78 行)的 format 校验加 jsonl:

```go
			if g.Format != "json" && g.Format != "table" && g.Format != "csv" && g.Format != "tsv" && g.Format != "jsonl" {
				return fmt.Errorf("invalid format %q (want json|table|csv|tsv|jsonl)", g.Format)
			}
```

flag 注册(原 80-93 行,在 `--limit` 那行之后)加:

```go
	pf.BoolVar(&g.NoLimit, "no-limit", false, "disable default row cap for SELECT (returns full result set)")
```

`--format` 的 help 文案(原 82 行)改为:

```go
	pf.StringVarP(&g.Format, "format", "f", "json", "output format: json|table|csv|tsv|jsonl")
```

- [ ] **Step 4: Add resolveCap / defaultCap / emitReadResult;wire newQueryCmd**

`internal/cli/commands.go` import 块(原 3-17 行)加 `"strconv"`(若没有)。

`resolve()`(原 27-43 行)在 `cfg, err = config.LoadFile(...)` 成功分支后、`over := ...` 之前加:

```go
		if cfg != nil {
			g.DefaultLimit = cfg.DefaultLimit
		}
```

(放在 `if _, err := os.Stat(...); err == nil { ... }` 块内,`LoadFile` 成功后。)

在 `opts()`(原 53-56 行)之后新增三个方法:

```go
// defaultCap resolves the default row cap: config > env > built-in 1000.
func (g *Globals) defaultCap() int {
	if g.DefaultLimit > 0 {
		return g.DefaultLimit
	}
	if v, err := strconv.Atoi(os.Getenv("MYSQL_CLI_DEFAULT_LIMIT")); err == nil && v > 0 {
		return v
	}
	return 1000
}

// resolveCap decides (limit, probe) for a read query:
//   --no-limit       -> (0, false)         no cap
//   --limit explicit -> (g.Limit, false)   exact N, no probe
//   otherwise        -> (defaultCap, true) default cap with cap+1 probe
func (g *Globals) resolveCap(cmd *cobra.Command) (int, bool) {
	if g.NoLimit {
		return 0, false
	}
	if cmd.Flags().Changed("limit") {
		return g.Limit, false
	}
	return g.defaultCap(), true
}

// emitReadResult renders a read query result: json -> ReadJSON (slim envelope),
// jsonl -> line stream + stderr truncated notice, else -> Format.
func (g *Globals) emitReadResult(r result.Result, err error, limit int) {
	if err != nil {
		fmt.Fprintln(g.out, formatErr(err, g.Format))
		return
	}
	switch g.Format {
	case "json":
		fmt.Fprint(g.out, format.ReadJSON(r, limit))
	case "jsonl":
		out, _ := format.Format(r, "jsonl")
		fmt.Fprint(g.out, out)
		if r.Truncated {
			fmt.Fprintf(g.eout, "# truncated:true limit:%d\n", limit)
		}
	default:
		out, _ := format.Format(r, g.Format)
		fmt.Fprint(g.out, out)
	}
}
```

`newQueryCmd` 的 RunE(原 92-104 行)switch 块改为:

```go
			ctx := context.Background()
			var r result.Result
			switch safety.Classify(sqlText) {
			case safety.CategoryRead, safety.CategoryUnknown:
				opts := g.opts()
				opts.Limit, opts.Probe = g.resolveCap(cmd)
				r, err = query.Execute(ctx, pool, sqlText, opts)
				g.emitReadResult(r, err, opts.Limit)
			default:
				r, err = query.ExecuteWrite(ctx, pool, sqlText, g.opts())
				g.emitResult(r, err)
			}
			return err
```

(删除原 `g.emitResult(r, err)` 统一调用;read 走 emitReadResult,write 走 emitResult。)

- [ ] **Step 5: Run all cli tests + full build**

Run: `go test ./internal/cli/ -v`
Expected: PASS(新测试 + 现有 cli 测试全过)

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 6: Commit**

```bash
git add internal/cli/root.go internal/cli/commands.go internal/cli/commands_test.go
git commit -m "feat(cli): --no-limit flag, default cap priority, read envelope routing"
```

---

### Task 6: skill 更新 + version bump

**Files:**
- Modify: `skills/mysql-shared/SKILL.md`(frontmatter version + 默认 cap/--no-limit/jsonl 章节)
- Modify: `skills/mysql-query/SKILL.md`(frontmatter version + agent 适配指引)

**Interfaces:**
- Produces: skill 文档更新;bundle `go build` 后重新 embed

- [ ] **Step 1: Update mysql-shared frontmatter + content**

`skills/mysql-shared/SKILL.md` frontmatter:

- `version: 1.0.0` -> `version: 1.1.0`
- `output_formats: json | table | csv | tsv` -> `output_formats: json | table | csv | tsv | jsonl`
- `safety_model:` 那行追加 `; SELECT 默认 cap 1000 (--no-limit 关)`

在 `## Output Formats / 输出格式` 章节(原 ~94 行)内追加一段:

```markdown
### 默认行数 cap / Default row cap

SELECT 不带 LIMIT 时,mysql-cli 默认只返回前 1000 行并在 `meta.truncated=true` 标记(用 cap+1 探测,零额外查询)。

- `--limit N`:显式要 N 行,精确返回,不探测截断
- `--no-limit`:关闭默认 cap,返回全表(危险,可能撑爆 context)
- `default_limit`(config.toml 顶层)/ `MYSQL_CLI_DEFAULT_LIMIT`(env):调默认 cap 值
- 优先级:`--limit` > `--no-limit` > config > env > 1000
- 见 `truncated:true` 时,要全量需 `--no-limit` 或先 `SELECT COUNT(*)` 评估

`--format jsonl`:每行一个 JSON 对象(`{"col":val,...}`),NULL 为 `null`,比 json 省 token;截断信息走 stderr。
```

- [ ] **Step 2: Update mysql-query frontmatter + content**

`skills/mysql-query/SKILL.md` frontmatter:`version: 1.0.0` -> `version: 1.1.0`。

在 `## Notes / 备注` 章节(原 ~129 行)追加:

```markdown
### 默认 cap 与截断

- SELECT 默认只返 1000 行;`meta.truncated=true` 表示被截断,需 `--no-limit` 或 `COUNT(*)` 评估全量后再决定。
- 省 token:`--format jsonl`(紧凑)或 `--format csv`;避免 `--format table`(对 agent 极费 token)。
- 确知要全表且可承受时才 `--no-limit`(实测 4.4 万行表裸跑 ≈900 万 token)。
```

- [ ] **Step 3: Validate skill frontmatter + rebuild bundle**

Run: `./scripts/skill-format-check.sh skills/`
Expected: 退出码 0,无报错

Run: `go build ./...`
Expected: 无错误(重新 embed 更新后的 skills/)

- [ ] **Step 4: Commit**

```bash
git add skills/mysql-shared/SKILL.md skills/mysql-query/SKILL.md
git commit -m "docs(skill): document default cap, --no-limit, jsonl; bump 1.1.0"
```

---

### Task 7: CHANGELOG + shootout 复测验证

**Files:**
- Create/Modify: `CHANGELOG.md`(若不存在则建)

**Interfaces:** 无(文档 + 手动验证)

- [ ] **Step 1: Add CHANGELOG entry**

`CHANGELOG.md` 顶部(无文件则新建)加:

```markdown
# Changelog

## [Unreleased]

### Breaking
- **SELECT 默认安全 cap 1000**:不带 LIMIT 的 SELECT 现在默认只返回 1000 行,`meta.truncated=true` 标记截断。需全表用 `--no-limit`;调默认值用 config `default_limit` 或 env `MYSQL_CLI_DEFAULT_LIMIT`;显式精确行数用 `--limit N`。动机:实测裸跑 4.4 万行表 = ~900 万 token(45 个 200K context 窗口),会当场撑爆 agent 会话。
- **SELECT 的 JSON 信封省略 `rows_affected`**(对 SELECT 恒为 0);改用 `meta.truncated`/`meta.limit`。DML/DDL 信封不变。

### Added
- `--format jsonl`:每行一个 JSON 对象,比 json 紧凑,适合 agent。
- `--no-limit` flag。
- config `default_limit` / env `MYSQL_CLI_DEFAULT_LIMIT`。
```

- [ ] **Step 2: Run full test suite**

Run: `go test ./...`
Expected: PASS(所有单测,默认跳过集成)

Run: `go test -cover ./...`
Expected: 覆盖率 ≥80%(项目历史区间 81%~92%)

- [ ] **Step 3: shootout 复测验证默认 cap 生效**

实现已编译安装(假设 `mysql-cli` 在 PATH 或用 `/Users/allenj/go/bin/mysql-cli`)。用 shootout 脚本对真实表验证:默认(无 --limit 无 --no-limit)应被截到 1000 行,`--no-limit` 仍全表。

Run(手动验证,连真实库):

```bash
CLI=/Users/allenj/go/bin/mysql-cli
export $(python3 -c "import json;e=json.load(open('.mcp.json'))['mcpServers']['mysql']['env'];print(' '.join(f'{k}={v}' for k,v in e.items()))")
# 默认 cap:应返回 meta.truncated=true,约 1000 行
"$CLI" query "SELECT * FROM sd_cx_order" --format json | python3 -c "import sys,json;d=json.load(sys.stdin);print('truncated=',d['meta']['truncated'],'rows=',len(d['data']['rows']))"
# --no-limit:应全表(44516 行),无 truncated
"$CLI" query "SELECT * FROM sd_cx_order" --no-limit --format json | python3 -c "import sys,json;d=json.load(sys.stdin);print('rows=',len(d['data']['rows']))"
# --limit 20:精确 20 行
"$CLI" query "SELECT * FROM sd_cx_order" --limit 20 --format json | python3 -c "import sys,json;d=json.load(sys.stdin);print('rows=',len(d['data']['rows']))"
```

Expected:
- 默认:`truncated=True rows=1000`
- `--no-limit`:`rows=44516`
- `--limit 20`:`rows=20`

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): breaking default cap + jsonl + --no-limit"
```

---

## Self-Review

**Spec coverage:**
- §1 默认 cap 机制 + cap+1 探测 + 边界(已带 LIMIT 不动、SHOW 不 wrap、--limit 不探测) -> Task 2
- §2.1 ReadJSON 省 rows_affected + meta.truncated + jsonl + stderr 截断 -> Task 4 + Task 5
- §2.2 config default_limit + env + 优先级 -> Task 3 + Task 5(defaultCap)
- §2.3 --no-limit flag + 不新增退出码 + breaking -> Task 5 + Task 7
- §2.4 skill 更新 + version bump -> Task 6
- §2.5 单测 + 集成 + shootout 复测 -> 各 Task 测试 + Task 7 Step 2-3

**Placeholder scan:** 无 TBD/TODO;每个 code step 给完整代码。

**Type consistency:** `applyLimit(sqlText, limit, probe)` 在 Task 2 定义,Task 2 Step 5 更新所有调用点;`Options.Probe` 定义后 `g.opts()` 未加 Probe(默认 false),Task 5 在 read 分支显式设 `opts.Probe`;`ReadJSON(r, limit)` 定义于 Task 4,Task 5 emitReadResult 调用一致;`emitReadResult`/`resolveCap`/`defaultCap` 定义于 Task 5,测试与实现签名一致。

**注:** `SHOW`/`DESCRIBE` 不 wrap 由 `selectRe = ^\s*(SELECT|WITH)\b` 天然保证(Task 2 不改 selectRe),无需额外代码。`schema` 子命令走 `schema` 包不经 `query.Execute`,本期不改其信封(保持 `emitResult`->`SuccessJSON`),与 spec"SELECT 走 ReadJSON"一致(schema 命令非 SELECT)。
