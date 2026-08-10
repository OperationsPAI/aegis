package tracing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRootSpanContext_RoundTrip(t *testing.T) {
	const (
		traceHex = "0102030405060708090a0b0c0d0e0f10"
		spanHex  = "1112131415161718"
		flags    = uint8(1)
	)

	sc, err := NewRootSpanContext(traceHex, spanHex, flags)
	require.NoError(t, err)
	assert.Equal(t, traceHex, sc.TraceID().String())
	assert.Equal(t, spanHex, sc.SpanID().String())
	assert.Equal(t, flags, byte(sc.TraceFlags()))
	assert.True(t, sc.IsValid())
	assert.True(t, sc.IsRemote())
}

func TestNewRootSpanContext_RejectsBadHex(t *testing.T) {
	_, err := NewRootSpanContext("zz", "1112131415161718", 1)
	assert.Error(t, err)

	_, err = NewRootSpanContext("0102030405060708090a0b0c0d0e0f10", "zz", 1)
	assert.Error(t, err)
}
