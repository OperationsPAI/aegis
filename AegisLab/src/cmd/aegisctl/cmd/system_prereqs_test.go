package cmd

import (
	"reflect"
	"testing"
)

func TestBuildHelmUpgradeInstallArgs_WithVersionAndValues(t *testing.T) {
	spec := helmPrereqSpec{
		Chart:     "coherence/coherence-operator",
		Namespace: "coherence-test",
		Version:   ">=3.5",
		Values: []helmPrereqSetValue{
			{Key: "image.registry", Value: "pair-cn-shanghai.cr.volces.com/opspai"},
			{Key: "image.name", Value: "coherence-operator"},
			{Key: "", Value: "ignored-empty-key"},
		},
	}

	got := buildHelmUpgradeInstallArgs("coherence-operator", spec)
	want := []string{
		"upgrade", "--install", "coherence-operator", "coherence/coherence-operator",
		"-n", "coherence-test", "--create-namespace",
		"--version", ">=3.5",
		"--set", "image.registry=pair-cn-shanghai.cr.volces.com/opspai",
		"--set", "image.name=coherence-operator",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("build args mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildHelmUpgradeInstallArgs_CompatibleWithoutValues(t *testing.T) {
	spec := helmPrereqSpec{
		Chart:     "coherence/coherence-operator",
		Namespace: "coherence-test",
	}

	got := buildHelmUpgradeInstallArgs("coherence-operator", spec)
	want := []string{
		"upgrade", "--install", "coherence-operator", "coherence/coherence-operator",
		"-n", "coherence-test", "--create-namespace",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("build args mismatch\n got: %#v\nwant: %#v", got, want)
	}
}
