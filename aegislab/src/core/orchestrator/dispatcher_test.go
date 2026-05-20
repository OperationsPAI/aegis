package consumer

import (
	"context"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/testutil"

	chaoshooks "aegis/crud/hooks/chaos"
)

// TestHandleCRDSucceededLabelParseClaimsGate pins the label-parsing → gate-
// claim path the CRD watcher takes. The earlier collision test poked the
// gate with a literal taskID; this drives the same identity through
// parseCRDLabels first so a regression in the label schema (e.g. renaming
// task_id) actually breaks the test.
func TestHandleCRDSucceededLabelParseClaimsGate(t *testing.T) {
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&chaoshooks.HookSubmission{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const taskID = "task-from-label-XYZ"
	labels := map[string]string{
		consts.JobLabelTaskID:    taskID,
		consts.JobLabelTaskType:  consts.GetTaskTypeName(consts.TaskTypeFaultInjection),
		consts.JobLabelTraceID:   "trace-1",
		consts.JobLabelGroupID:   "group-1",
		consts.JobLabelProjectID: "1",
		consts.JobLabelUserID:    "1",
		consts.CRDLabelBatchID:   "batch-1",
		consts.CRDLabelIsHybrid:  "false",
	}

	parsed, err := parseCRDLabels(labels)
	if err != nil {
		t.Fatalf("parseCRDLabels: %v", err)
	}
	if parsed.taskID != taskID {
		t.Fatalf("parsed taskID: want %q got %q", taskID, parsed.taskID)
	}

	claimed, err := chaoshooks.ClaimBuildDatapackGate(context.Background(), db, parsed.taskID, parsed.IsHybrid, "succeeded")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed {
		t.Fatal("first claim returned false; gate must accept first writer")
	}

	claimedAgain, err := chaoshooks.ClaimBuildDatapackGate(context.Background(), db, parsed.taskID, parsed.IsHybrid, "succeeded")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimedAgain {
		t.Fatal("second claim returned true; gate must reject duplicates")
	}
}

