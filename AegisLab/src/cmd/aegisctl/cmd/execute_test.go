package cmd

import (
	"encoding/json"
	"testing"

	"aegis/cmd/aegisctl/client"
)

func TestDecodeExecuteListResponse(t *testing.T) {
	payload := `{"code":0,"message":"success","data":{"items":[{"id":508,"algorithm":"","datapack":"","state":"success","duration":34.176802,"created_at":"2026-04-27T17:26:46.299+08:00"}],"pagination":{"page":1,"size":20,"total":508,"total_pages":26}}}`

	var resp client.APIResponse[client.PaginatedData[executeListItem]]
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("decode execute list response: %v", err)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected item count: %d", len(resp.Data.Items))
	}
	if got := resp.Data.Items[0].Duration; got != 34.176802 {
		t.Fatalf("unexpected duration value: %v", got)
	}
}
