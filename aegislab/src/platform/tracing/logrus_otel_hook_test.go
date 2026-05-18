package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/logtest"
)

// captureHook builds a hook backed by a logtest Recorder and returns the
// recorder so the test can inspect emitted records. Keeping construction
// here (rather than spreading recorder boilerplate across each case) makes
// the assertions read like specifications instead of plumbing.
func captureHook() (*OtelLogrusHook, *logtest.Recorder) {
	rec := logtest.NewRecorder()
	hook := newHookWithLogger(rec.Logger("aegis/logrus-bridge"))
	return hook, rec
}

func recordedAttr(t *testing.T, r log.Record, key string) (string, bool) {
	t.Helper()
	var (
		out   string
		found bool
	)
	r.WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key == key {
			out = kv.Value.AsString()
			found = true
			return false
		}
		return true
	})
	return out, found
}

func TestOtelLogrusHookFiresWithFields(t *testing.T) {
	hook, rec := captureHook()

	log0 := logrus.New()
	log0.SetOutput(discardWriter{})
	log0.AddHook(hook)

	log0.WithFields(logrus.Fields{
		"task_id":  "task-42",
		"trace_id": "abc-123",
	}).Info("hello world")

	scopes := rec.Result()
	require.Len(t, scopes, 1)
	require.Len(t, scopes[0].Records, 1)

	got := scopes[0].Records[0]
	require.Equal(t, "hello world", got.Body().AsString())
	require.Equal(t, log.SeverityInfo1, got.Severity())
	require.Equal(t, logrus.InfoLevel.String(), got.SeverityText())

	taskID, ok := recordedAttr(t, got, "task_id")
	require.True(t, ok, "task_id attribute missing")
	require.Equal(t, "task-42", taskID)

	traceID, ok := recordedAttr(t, got, "trace_id")
	require.True(t, ok, "trace_id attribute missing")
	require.Equal(t, "abc-123", traceID)
}

func TestOtelLogrusHookPropagatesContext(t *testing.T) {
	hook, rec := captureHook()

	log0 := logrus.New()
	log0.SetOutput(discardWriter{})
	log0.AddHook(hook)

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("k"), "v")
	log0.WithContext(ctx).Warn("with-ctx")

	scopes := rec.Result()
	require.Len(t, scopes, 1)
	require.Len(t, scopes[0].Records, 1)
	got := scopes[0].Records[0]
	require.Equal(t, "with-ctx", got.Body().AsString())
	require.Equal(t, log.SeverityWarn1, got.Severity())
}

func TestToOTelSeverityMapping(t *testing.T) {
	cases := []struct {
		in   logrus.Level
		want log.Severity
	}{
		{logrus.TraceLevel, log.SeverityTrace1},
		{logrus.DebugLevel, log.SeverityDebug1},
		{logrus.InfoLevel, log.SeverityInfo1},
		{logrus.WarnLevel, log.SeverityWarn1},
		{logrus.ErrorLevel, log.SeverityError1},
		{logrus.FatalLevel, log.SeverityFatal1},
		{logrus.PanicLevel, log.SeverityFatal2},
	}
	for _, c := range cases {
		require.Equal(t, c.want, toOTelSeverity(c.in), "level %s", c.in)
	}
}

func TestOtelLogrusHookTimestampPreserved(t *testing.T) {
	hook, rec := captureHook()
	// Fire the entry directly so we can pin Time without racing logrus'
	// own clock — verifies the hook copies e.Time onto the record rather
	// than stamping time.Now() at Fire time.
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	err := hook.Fire(&logrus.Entry{Level: logrus.InfoLevel, Time: when, Message: "ts-check"})
	require.NoError(t, err)

	scopes := rec.Result()
	require.Len(t, scopes, 1)
	require.Len(t, scopes[0].Records, 1)
	require.True(t, scopes[0].Records[0].Timestamp().Equal(when))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
