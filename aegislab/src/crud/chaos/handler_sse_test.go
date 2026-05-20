package chaos

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestStreamInjectionEvents_DrivesPendingRunningSucceeded asserts the
// on-the-wire shape: initial pending event, a running transition, and a
// terminal succeeded event with `event: terminal`, in order. Polling
// cadence is shorter than sseStreamPollInterval so the driver loop can
// race the ticker reliably under -count=N.
func TestStreamInjectionEvents_DrivesPendingRunningSucceeded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, _, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)

	inj, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID:        pointID,
		Namespace:      "ns0",
		Params:         map[string]any{"duration_s": 30},
		IdempotencyKey: "key-sse-1",
	})
	if err != nil {
		t.Fatalf("seed injection: %v", err)
	}
	// CreateInjection currently lands at "running" (fakeExecutor Apply
	// succeeds). Roll the row back to pending so the test can drive the
	// full pending → running → succeeded transition.
	if err := db.Model(&Injection{}).Where("id = ?", inj.ID).
		Updates(map[string]any{"status": StatusPending, "started_at": nil, "finished_at": nil}).Error; err != nil {
		t.Fatalf("reset to pending: %v", err)
	}

	r := gin.New()
	h := &Handler{Mgr: mgr}
	r.GET("/v1beta/injections/:id/events", h.StreamInjectionEvents)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1beta/injections/" + inj.ID + "/events")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type: %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	readEvent := func() (eventName string, data injectionEvent, err error) {
		for {
			line, rErr := reader.ReadString('\n')
			if rErr != nil {
				return "", injectionEvent{}, rErr
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if data.InjectionID != "" || eventName != "" {
					return eventName, data, nil
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				eventName = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				payload := strings.TrimPrefix(line, "data: ")
				if jErr := json.Unmarshal([]byte(payload), &data); jErr != nil {
					return "", injectionEvent{}, jErr
				}
			}
		}
	}

	// Initial event: pending.
	_, first, err := readEvent()
	if err != nil {
		t.Fatalf("read first event: %v", err)
	}
	if first.Status != StatusPending || first.InjectionID != inj.ID || first.Attempt != 1 {
		t.Fatalf("first event mismatch: %+v", first)
	}

	// Flip to running, expect a running event.
	if err := db.Model(&Injection{}).Where("id = ?", inj.ID).
		Update("status", StatusRunning).Error; err != nil {
		t.Fatalf("flip running: %v", err)
	}
	_, second, err := readEvent()
	if err != nil {
		t.Fatalf("read second event: %v", err)
	}
	if second.Status != StatusRunning || second.Attempt != 2 {
		t.Fatalf("second event mismatch: %+v", second)
	}

	// Flip to terminal succeeded, expect a third event marked terminal.
	now := time.Now().UTC()
	if err := db.Model(&Injection{}).Where("id = ?", inj.ID).
		Updates(map[string]any{"status": StatusSucceeded, "finished_at": &now}).Error; err != nil {
		t.Fatalf("flip succeeded: %v", err)
	}
	ev3, third, err := readEvent()
	if err != nil {
		t.Fatalf("read third event: %v", err)
	}
	if ev3 != "terminal" {
		t.Fatalf("expected event: terminal, got %q", ev3)
	}
	if third.Status != StatusSucceeded || third.Attempt != 3 {
		t.Fatalf("third event mismatch: %+v", third)
	}

	// Server must close the body after the terminal event.
	tail, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("drain tail: %v", err)
	}
	if len(strings.TrimSpace(string(tail))) != 0 {
		t.Fatalf("unexpected trailing bytes after terminal: %q", tail)
	}
}
