package repl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/chzyer/readline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatchQuit(t *testing.T) {
	code, _ := dispatch("\\q", Config{})
	assert.Equal(t, -1, code) // -1 signals exit
}

func TestDispatchUnknownSlash(t *testing.T) {
	_, msg := dispatch("\\bogus", Config{})
	assert.Contains(t, msg, "unknown")
}

func TestIsExit(t *testing.T) {
	assert.True(t, isExit(-1))
	assert.False(t, isExit(0))
}

func TestRunOnceQuit(t *testing.T) {
	assert.True(t, runOnce("\\q", Config{}))
}

// TestRunOnceUnknownInputNoSilentDrop（B2 回归）：unknown 类输入不再被
// looksLikeSQL 门静默丢弃。未连接时给出 "not connected" 提示；已连接时由
// safety 闸门给出明确错误（unknown 语句需 --write）。
func TestRunOnceUnknownInputNotConnected(t *testing.T) {
	var buf bytes.Buffer
	assert.False(t, runOnce("hello world", Config{Out: &buf}))
	assert.Contains(t, buf.String(), "not connected")
}

func TestRunOnceUnknownInputGuardError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	var buf bytes.Buffer
	pool := &conn.Pool{DB: db}
	cfg := Config{Pool: pool, Out: &buf, Format: "json"}
	assert.False(t, runOnce("hello world", cfg))
	assert.Contains(t, buf.String(), "requires --write")
	assert.NotContains(t, buf.String(), `"success":true`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRunOnceCTENotSilentDropped（B2 核心）：WITH 开头的 CTE 在 safety 中
// 归 CategoryUnknown，曾因 looksLikeSQL 门被静默丢弃；现在走 runSQL 的
// unknown 分支，得到明确错误（保守闸门要求 --write）。
func TestRunOnceCTENotSilentDropped(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	var buf bytes.Buffer
	pool := &conn.Pool{DB: db}
	cfg := Config{Pool: pool, Out: &buf, Format: "json"}
	assert.False(t, runOnce("WITH c AS (SELECT 1) SELECT * FROM c", cfg))
	assert.Contains(t, buf.String(), "requires --write")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnceSQLRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	var buf bytes.Buffer
	pool := &conn.Pool{DB: db}
	cfg := Config{Pool: pool, Out: &buf, Format: "json"}
	assert.False(t, runOnce("SELECT 1", cfg))
	assert.Contains(t, buf.String(), `"success":true`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnceSQLWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t SET a=1 WHERE id=1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	var buf bytes.Buffer
	pool := &conn.Pool{DB: db}
	cfg := Config{
		Pool:   pool,
		Out:    &buf,
		Opts:   query.Options{Write: true},
		Format: "json",
	}
	assert.False(t, runOnce("UPDATE t SET a=1 WHERE id=1", cfg))
	assert.Contains(t, buf.String(), `"rows_affected":1`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStartInitFailure（B4）：readline 初始化失败时 Start 返回非零 code 和
// 携带 ErrInitFailed 哨兵的 error，cli 层据此退出非零而非伪装成功。
func TestStartInitFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // historyPath 不触碰真实 home
	orig := newReadline
	newReadline = func(cfg *readline.Config) (*readline.Instance, error) {
		return nil, errors.New("tty boom")
	}
	t.Cleanup(func() { newReadline = orig })

	code, err := Start(Config{})
	assert.Equal(t, 1, code)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInitFailed)
	assert.Contains(t, err.Error(), "tty boom")
}

// TestHistoryPath（B7）：历史文件落在 ~/.config/mysql-cli/history，目录
// 0700、文件 0600，不再写多用户可读的 /tmp。
func TestHistoryPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := historyPath()
	require.NotEmpty(t, p)
	assert.Equal(t, filepath.Join(home, ".config", "mysql-cli", "history"), p)

	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	dinfo, err := os.Stat(filepath.Join(home, ".config", "mysql-cli"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dinfo.Mode().Perm())
}

// TestHistoryPathTightensPerms（B7）：已存在的宽权限历史文件被收紧到 0600。
func TestHistoryPathTightensPerms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "mysql-cli")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	p := filepath.Join(dir, "history")
	require.NoError(t, os.WriteFile(p, []byte(""), 0o644))

	assert.Equal(t, p, historyPath())
	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestHistoryPathNoHome（B7）：home 不可用时降级为不落盘历史（返回 ""），
// 不报错崩掉 REPL。
func TestHistoryPathNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	assert.Empty(t, historyPath())
}

// TestRemoveLegacyHistory（F6）：旧版 /tmp/mysql-cli.history 被 best-effort
// 清理；文件不存在或路径为空时为静默 no-op。
func TestRemoveLegacyHistory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mysql-cli.history")
	require.NoError(t, os.WriteFile(p, []byte("SELECT secret"), 0o644))

	removeLegacyHistory(p)
	_, err := os.Stat(p)
	assert.True(t, os.IsNotExist(err), "legacy history file removed")

	// 不存在 / 空路径：静默 no-op，不 panic 不报错。
	removeLegacyHistory(filepath.Join(dir, "nope"))
	removeLegacyHistory("")
}

// TestStartRemovesLegacyHistory（F6）：Start 启动时清理旧版历史文件；即便
// readline 初始化失败，清理也已先行发生。
func TestStartRemovesLegacyHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := t.TempDir()
	p := filepath.Join(dir, "mysql-cli.history")
	require.NoError(t, os.WriteFile(p, []byte("SELECT secret"), 0o644))

	origPath, origNew := legacyHistoryPath, newReadline
	legacyHistoryPath = p
	newReadline = func(cfg *readline.Config) (*readline.Instance, error) {
		return nil, errors.New("tty boom")
	}
	t.Cleanup(func() {
		legacyHistoryPath = origPath
		newReadline = origNew
	})

	_, _ = Start(Config{})
	_, err := os.Stat(p)
	assert.True(t, os.IsNotExist(err), "legacy history cleaned when Start runs")
}
