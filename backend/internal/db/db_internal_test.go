package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptionalChunk(t *testing.T) {
	feature, ok := optionalChunk("\n-- header\n-- optional: hypertables\nSELECT 1;")
	assert.True(t, ok)
	assert.Equal(t, "hypertables", feature)

	_, ok = optionalChunk("\nCREATE TABLE t (id int);\n-- optional: not leading\n")
	assert.False(t, ok)
	_, ok = optionalChunk("-- plain comment\nSELECT 1;")
	assert.False(t, ok)
	_, ok = optionalChunk("   \n")
	assert.False(t, ok)
}
