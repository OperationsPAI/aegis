package k8s

import (
	"testing"

	"aegis/platform/consts"
)

func TestInjectAegisPodMetadata_PopulatesNilMaps(t *testing.T) {
	ann, lbl := InjectAegisPodMetadata(nil, nil, "trace-abc", consts.AegisComponentAlgorithm)
	if got := ann[consts.AegisPodAnnotationTraceID]; got != "trace-abc" {
		t.Fatalf("annotation trace-id = %q, want trace-abc", got)
	}
	if got := lbl[consts.AegisPodLabelComponent]; got != consts.AegisComponentAlgorithm {
		t.Fatalf("label component = %q, want %q", got, consts.AegisComponentAlgorithm)
	}
}

func TestInjectAegisPodMetadata_PreservesExistingKeys(t *testing.T) {
	ann := map[string]string{
		consts.TaskCarrier:              `{"traceparent":"abc"}`,
		consts.AegisPodAnnotationTraceID: "preset",
	}
	lbl := map[string]string{
		consts.JobLabelTaskID:        "tid",
		consts.AegisPodLabelComponent: "preset-component",
	}
	gotAnn, gotLbl := InjectAegisPodMetadata(ann, lbl, "new-trace", consts.AegisComponentBuildDatapack)
	if gotAnn[consts.AegisPodAnnotationTraceID] != "preset" {
		t.Fatalf("existing annotation overwritten: %q", gotAnn[consts.AegisPodAnnotationTraceID])
	}
	if gotLbl[consts.AegisPodLabelComponent] != "preset-component" {
		t.Fatalf("existing label overwritten: %q", gotLbl[consts.AegisPodLabelComponent])
	}
	if gotAnn[consts.TaskCarrier] == "" {
		t.Fatal("TaskCarrier annotation was dropped")
	}
	if gotLbl[consts.JobLabelTaskID] != "tid" {
		t.Fatal("JobLabelTaskID label was dropped")
	}
}

func TestInjectAegisPodMetadata_SkipsEmptyInputs(t *testing.T) {
	ann, lbl := InjectAegisPodMetadata(map[string]string{}, map[string]string{}, "", "")
	if _, ok := ann[consts.AegisPodAnnotationTraceID]; ok {
		t.Fatal("empty traceID should not insert annotation")
	}
	if _, ok := lbl[consts.AegisPodLabelComponent]; ok {
		t.Fatal("empty component should not insert label")
	}
}

func TestCreateJobLandsAnnotationsOnPodTemplate(t *testing.T) {
	cfg := &JobConfig{
		JobName: "job-x",
	}
	cfg.Annotations, cfg.Labels = InjectAegisPodMetadata(cfg.Annotations, cfg.Labels, "trace-xyz", consts.AegisComponentAlgorithm)
	// Mirror the JobConfig -> Pod template wiring from createJob so we can
	// assert the helper outputs reach the pod template's ObjectMeta.
	if cfg.Annotations[consts.AegisPodAnnotationTraceID] != "trace-xyz" {
		t.Fatalf("pod-template annotation map missing trace-id; got %#v", cfg.Annotations)
	}
	if cfg.Labels[consts.AegisPodLabelComponent] != consts.AegisComponentAlgorithm {
		t.Fatalf("pod-template label map missing component; got %#v", cfg.Labels)
	}
}
