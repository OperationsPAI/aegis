package chaos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"aegis/platform/testutil"
)

type fakeBackendTokenSource struct {
	tok string
	err error
}

func (f fakeBackendTokenSource) ServiceToken(context.Context) (string, error) {
	return f.tok, f.err
}

// TestWebhookTokenProvisioner_MintAndSenderReadsDynamic proves the two halves
// of the self-provisioning flow: (1) the provisioner authenticates the SSO
// issue call with the aegis-backend bearer and caches the minted chaos-service
// token, and (2) the WebhookSender sends that dynamic token per request,
// preferring it over the static CHAOS_SA_TOKEN env fallback.
func TestWebhookTokenProvisioner_MintAndSenderReadsDynamic(t *testing.T) {
	const (
		backendTok = "backend-bearer-xyz"
		issuedTok  = "minted-chaos-service-jwt"
	)

	var sawIssueAuth atomic.Value
	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/service-accounts/chaos-service/issue" || r.Method != http.MethodPost {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		sawIssueAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"token":"` + issuedTok + `","expires_at":"2030-01-01T00:00:00Z"}}`))
	}))
	t.Cleanup(sso.Close)

	p := NewWebhookTokenProvisioner(fakeBackendTokenSource{tok: backendTok}, sso.URL)
	if err := p.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := p.Token(); got != issuedTok {
		t.Fatalf("Token() = %q, want %q", got, issuedTok)
	}
	if got, _ := sawIssueAuth.Load().(string); got != "Bearer "+backendTok {
		t.Fatalf("issue Authorization = %q, want %q", got, "Bearer "+backendTok)
	}

	// Now wire the sender: dynamic provider + static fallback. The receiver
	// must observe the dynamic token, never the static one.
	var sawWebhookAuth atomic.Value
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawWebhookAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recv.Close)

	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&Injection{}); err != nil {
		t.Fatal(err)
	}
	inj := Injection{ID: "inj1", IdempotencyKey: "k1", Status: StatusSucceeded, Params: JSONMap{}, Ts: time.Now().UTC()}
	if err := db.Create(&inj).Error; err != nil {
		t.Fatal(err)
	}

	sender := NewWebhookSender(recv.Client(), recv.URL, db, nil)
	sender.SetBearer("static-fallback-token")
	sender.SetTokenProvider(p.Token)

	if err := sender.Fire(context.Background(), &inj); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if got, _ := sawWebhookAuth.Load().(string); got != "Bearer "+issuedTok {
		t.Fatalf("webhook Authorization = %q, want dynamic %q", got, "Bearer "+issuedTok)
	}
}

// TestWebhookSender_FallsBackToStaticBearer confirms the env fallback survives:
// when the provisioner has no token yet, the sender sends the static bearer.
func TestWebhookSender_FallsBackToStaticBearer(t *testing.T) {
	var sawAuth atomic.Value
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recv.Close)

	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&Injection{}); err != nil {
		t.Fatal(err)
	}
	inj := Injection{ID: "inj2", IdempotencyKey: "k2", Status: StatusSucceeded, Params: JSONMap{}, Ts: time.Now().UTC()}
	if err := db.Create(&inj).Error; err != nil {
		t.Fatal(err)
	}

	sender := NewWebhookSender(recv.Client(), recv.URL, db, nil)
	sender.SetBearer("static-fallback-token")
	// Provider present but empty (boot mint not yet completed).
	sender.SetTokenProvider(func() string { return "" })

	if err := sender.Fire(context.Background(), &inj); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if got, _ := sawAuth.Load().(string); got != "Bearer static-fallback-token" {
		t.Fatalf("webhook Authorization = %q, want static fallback", got)
	}
}
