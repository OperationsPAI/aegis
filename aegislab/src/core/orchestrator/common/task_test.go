package common

import (
	"testing"

	"aegis/platform/consts"
	"aegis/platform/dto"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

// TestLogMissingParentSeverity locks in §11 step 5b cleanup #3: the
// missing-parent log on a chaos-webhook submit is WARN when the parent
// was an aegisctl --via-chaos client-generated UUID (expected), and
// ERROR when the parent came from the backend dispatcher (regression,
// since the dispatcher persists the tasks row before POSTing).
func TestLogMissingParentSeverity(t *testing.T) {
	parent := "parent-task-id"
	cases := []struct {
		name     string
		extra    map[consts.TaskExtra]any
		wantLvl  logrus.Level
		wantFlag bool
	}{
		{
			name:     "aegisctl via-chaos hook submits => WARN",
			extra:    map[consts.TaskExtra]any{consts.TaskExtraParentSubmittedByAegisctlViaChaos: true},
			wantLvl:  logrus.WarnLevel,
			wantFlag: true,
		},
		{
			name:     "backend dispatcher submits => ERROR",
			extra:    nil,
			wantLvl:  logrus.ErrorLevel,
			wantFlag: false,
		},
		{
			name: "extra present but flag false => ERROR",
			extra: map[consts.TaskExtra]any{
				consts.TaskExtraParentSubmittedByAegisctlViaChaos: false,
			},
			wantLvl:  logrus.ErrorLevel,
			wantFlag: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hook := logrustest.NewGlobal()
			defer hook.Reset()

			t.Cleanup(func() {
				logrus.StandardLogger().Hooks = make(logrus.LevelHooks)
			})

			task := &dto.UnifiedTask{
				TaskID:       "task-A",
				Type:         consts.TaskTypeBuildDatapack,
				ParentTaskID: &parent,
				Extra:        tc.extra,
			}
			logMissingParent(task)

			if len(hook.Entries) != 1 {
				t.Fatalf("want exactly 1 log entry, got %d", len(hook.Entries))
			}
			entry := hook.Entries[0]
			if entry.Level != tc.wantLvl {
				t.Fatalf("want level %v, got %v (msg=%q)", tc.wantLvl, entry.Level, entry.Message)
			}
			got, ok := entry.Data["via_aegisctl_chaos_hook"]
			if !ok {
				t.Fatalf("missing via_aegisctl_chaos_hook field; data=%+v", entry.Data)
			}
			if b, _ := got.(bool); b != tc.wantFlag {
				t.Fatalf("want via_aegisctl_chaos_hook=%v, got %v", tc.wantFlag, b)
			}
			if got, _ := entry.Data["parent_task_id"].(string); got != parent {
				t.Fatalf("want parent_task_id=%q, got %v", parent, entry.Data["parent_task_id"])
			}
		})
	}
}
