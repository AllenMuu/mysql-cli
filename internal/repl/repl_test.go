package repl

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestLooksLikeSQL(t *testing.T) {
	assert.True(t, looksLikeSQL("SELECT 1"))
	assert.False(t, looksLikeSQL("\\tables"))
}
