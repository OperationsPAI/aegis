package pedestal

import (
	"context"
	"errors"
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/helm"
	"aegis/platform/model"
)

// fakeHelmGateway captures the calls the service makes against the helm
// SDK so the chart-resolution paths can be exercised without a real
// Kubernetes cluster. Only the read methods used by ListReleases /
// GetRelease are implemented; Install / Restart are out of scope for this
// test file (they call into helm.InstallPedestal which requires a real
// kubeconfig + cluster).
type fakeHelmGateway struct {
	releases []helm.ReleaseInfo
	infos    map[string]*helm.ReleaseInfo
	values   map[string]map[string]any
}

func (f *fakeHelmGateway) List(_ context.Context, _ ...string) ([]helm.ReleaseInfo, error) {
	return f.releases, nil
}

func (f *fakeHelmGateway) GetReleaseInfo(namespace, release string) (*helm.ReleaseInfo, error) {
	if info, ok := f.infos[namespace+"/"+release]; ok {
		return info, nil
	}
	return nil, nil
}

func (f *fakeHelmGateway) GetReleaseValues(namespace, release string) (map[string]any, error) {
	if v, ok := f.values[namespace+"/"+release]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeHelmGateway) Uninstall(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (f *fakeHelmGateway) IsReleaseDeployed(_ string, _ string) (bool, error) {
	return false, nil
}

// fakeSystemConfigSource returns a fixed shortcode → ns_pattern map so the
// list classification logic is deterministic.
type fakeSystemConfigSource map[string]string

func (f fakeSystemConfigSource) AllSystems() map[string]string { return f }

// TestListReleases_ClassifiesByNameAndNamespacePattern verifies the three
// classification outcomes: managed (name + ns_pattern match), name-only
// match (returns Reason explaining the namespace mismatch), and unknown
// (release name doesn't match any system).
func TestListReleases_ClassifiesByNameAndNamespacePattern(t *testing.T) {
	gw := &fakeHelmGateway{
		releases: []helm.ReleaseInfo{
			{Name: "ts", Namespace: "ts-1", Status: "deployed"},                // matches system "ts" with ns_pattern ^ts-\d+$
			{Name: "ts", Namespace: "weird-ns", Status: "deployed"},            // matches name but not pattern
			{Name: "unknown-chart", Namespace: "random", Status: "deployed"},   // no system at all
			{Name: "hs", Namespace: "hs", Status: "deployed"},                  // matches system "hs" with empty pattern → managed
		},
	}
	s := &RuntimeService{
		gateway: gw,
		systems: fakeSystemConfigSource{
			"ts": `^ts-\d+$`,
			"hs": ``,
		},
	}

	got, err := s.ListReleases(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(got))
	}

	// ts/ts-1: managed
	if !got[0].Managed || got[0].System != "ts" || got[0].Reason != "" {
		t.Errorf("ts/ts-1 want managed=true system=ts reason=''; got %+v", got[0])
	}
	// ts/weird-ns: name match, namespace mismatch → unmanaged with reason
	if got[1].Managed || got[1].System != "ts" || got[1].Reason == "" {
		t.Errorf("ts/weird-ns want managed=false system=ts reason!=''; got %+v", got[1])
	}
	// unknown-chart: not in system map
	if got[2].Managed || got[2].System != "" {
		t.Errorf("unknown-chart want managed=false system=''; got %+v", got[2])
	}
	// hs/hs: empty pattern → trust name alone
	if !got[3].Managed || got[3].System != "hs" {
		t.Errorf("hs/hs want managed=true system=hs; got %+v", got[3])
	}
}

// TestBuildInstallSpec_MergesValuesAndResolvesRepo confirms two service
// guarantees:
//
//   - Caller-supplied overrides win over HelmConfig.DynamicValues defaults
//     at the top-level key (shallow overwrite — matches helm --set).
//   - When HelmConfig.RepoURL is empty, resolveRepoURL falls back to the
//     etcd-backed `helm.repo.<name>.url` config (test goes through the
//     happy-path of RepoURL set since there's no Viper fixture here).
func TestBuildInstallSpec_MergesValuesAndResolvesRepo(t *testing.T) {
	def := "img-default"
	version := &model.ContainerVersion{
		ID: 42,
		HelmConfig: &model.HelmConfig{
			ID:        7,
			ChartName: "trainticket",
			Version:   "0.3.1",
			RepoURL:   "https://charts.example.com",
			RepoName:  "operations-pai",
			LocalPath: "/tmp/trainticket-0.3.1.tgz",
			DynamicValues: []model.ParameterConfig{
				{Key: "image.tag", DefaultValue: &def},
			},
		},
	}
	s := &RuntimeService{}

	overrides := map[string]any{
		"image": map[string]any{"tag": "custom"},
		"extra": "value",
	}
	spec, err := s.buildInstallSpec(version, "ts", "ts", overrides)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if spec.ReleaseName != "ts" || spec.Namespace != "ts" {
		t.Errorf("release/namespace want ts/ts; got %s/%s", spec.ReleaseName, spec.Namespace)
	}
	if spec.ChartName != "trainticket" || spec.Version != "0.3.1" {
		t.Errorf("chart/version want trainticket/0.3.1; got %s/%s", spec.ChartName, spec.Version)
	}
	if spec.RepoURL != "https://charts.example.com" || spec.RepoName != "operations-pai" {
		t.Errorf("repo want example.com/operations-pai; got %s/%s", spec.RepoURL, spec.RepoName)
	}
	if spec.LocalPath != "/tmp/trainticket-0.3.1.tgz" {
		t.Errorf("local_path want /tmp/trainticket-0.3.1.tgz; got %s", spec.LocalPath)
	}

	// Override map[string]any{"image":{"tag":"custom"}} should win over the
	// DynamicValues image.tag default. After shallow-overwrite, values[image]
	// is the override map (the DynamicValues map is replaced wholesale at
	// the top-level "image" key — documented merge semantics).
	imageVal, ok := spec.Values["image"]
	if !ok {
		t.Fatalf("expected image key in values; got %+v", spec.Values)
	}
	imageMap, ok := imageVal.(map[string]any)
	if !ok {
		t.Fatalf("expected image to be a map; got %T", imageVal)
	}
	if tag, _ := imageMap["tag"].(string); tag != "custom" {
		t.Errorf("expected image.tag=custom after override; got %v", imageMap["tag"])
	}
	if spec.Values["extra"] != "value" {
		t.Errorf("expected extra=value passthrough; got %v", spec.Values["extra"])
	}

	if spec.OverallTimeout <= 0 || spec.WaitTimeout <= 0 {
		t.Errorf("expected positive timeouts; got overall=%s wait=%s", spec.OverallTimeout, spec.WaitTimeout)
	}
}

// TestInstall_RejectsNonPedestalContainer ensures the request-level
// validation prevents an admin from accidentally installing a non-pedestal
// container version through this endpoint.
func TestInstall_RejectsBadInputsWithoutDBCalls(t *testing.T) {
	s := &RuntimeService{}

	cases := []struct {
		name string
		in   InstallPedestalInput
	}{
		{"missing system_code", InstallPedestalInput{ContainerVersionID: 1}},
		{"missing version_id", InstallPedestalInput{SystemCode: "ts"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Install(context.Background(), tc.in)
			if err == nil {
				t.Fatalf("expected error for %q; got nil", tc.name)
			}
			if !errors.Is(err, consts.ErrBadRequest) {
				t.Errorf("expected ErrBadRequest; got %v", err)
			}
		})
	}
}
