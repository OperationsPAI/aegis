package cmd

import (
	"encoding/json"
	"testing"

	"aegis/cmd/aegisctl/client"
)

func TestDecodeExecuteListResponse(t *testing.T) {
	payload := `{"code":0,"message":"success","data":{"items":[{"id":101,"algorithm":"Traceback","datapack":"pair_diagnosis","state":"Success","duration":3,"created_at":"2026-04-26T08:12:24Z"}],"pagination":{"page":1,"size":20,"total":1,"total_pages":1}}}`

	var resp client.APIResponse[client.PaginatedData[executeListItem]]
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("decode execute list response: %v", err)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected item count: %d", len(resp.Data.Items))
	}
	if got := resp.Data.Items[0].Duration; got != 3 {
		t.Fatalf("unexpected duration value: %v", got)
	}
}
