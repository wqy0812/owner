package api

import (
	"codex/platform-demo/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestComponentsSummaryDetailContractViewsAndRoles(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	users, err := f.database.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checked := map[domain.Role]bool{}
	for _, user := range users {
		if checked[user.Role] {
			continue
		}
		checked[user.Role] = true
		cookie := f.session(user.ID)
		response := f.request(http.MethodGet, "/api/v1/components", nil, cookie)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		summaries := decodeEnvelope(t, response)["items"].([]any)
		contractsResponse := f.request(http.MethodGet, "/api/v1/components?view=contracts", nil, cookie)
		if contractsResponse.Code != 200 {
			t.Fatal(contractsResponse.Body.String())
		}
		contracts := decodeEnvelope(t, contractsResponse)["items"].([]any)
		if len(summaries) != len(contracts) {
			t.Fatal("visibility mismatch")
		}
		for i, item := range summaries {
			summary := item.(map[string]any)
			contract := contracts[i].(map[string]any)
			if _, ok := summary["releases"]; ok {
				t.Fatal("directory contains contracts")
			}
			if _, ok := summary["releaseLines"]; ok {
				t.Fatal("directory contains lines")
			}
			releases, _ := contract["releases"].([]any)
			if summary["releaseCount"] != float64(len(releases)) {
				t.Fatal("count mismatch")
			}
			for _, line := range contract["releaseLines"].([]any) {
				metadata := line.(map[string]any)
				if _, ok := metadata["releases"]; ok {
					t.Fatal("duplicate releases")
				}
				if _, ok := metadata["releaseIds"]; !ok {
					t.Fatal("missing IDs")
				}
			}
			detail := f.request(http.MethodGet, "/api/v1/components/"+summary["id"].(string), nil, cookie)
			if detail.Code != 200 {
				t.Fatal(detail.Body.String())
			}
			data := decodeEnvelope(t, detail)["data"].(map[string]any)
			byID := func(value any) map[string]any {
				out := map[string]any{}
				for _, item := range value.([]any) {
					release := item.(map[string]any)
					out[release["id"].(string)] = release
				}
				return out
			}
			if !reflect.DeepEqual(byID(data["releases"]), byID(contract["releases"])) {
				left, right := byID(data["releases"]), byID(contract["releases"])
				for id, raw := range left {
					for key, value := range raw.(map[string]any) {
						if other, ok := right[id].(map[string]any); ok && !reflect.DeepEqual(value, other[key]) {
							t.Errorf("%s %s.%s detail=%v contracts=%v", user.Role, id, key, value, other[key])
						}
					}
				}
				t.Fatal("detail changed contracts or visibility")
			}
			if _, ok := data["readContext"]; !ok {
				t.Fatal("missing evidence context")
			}
		}
	}
	if len(checked) != 4 {
		t.Fatalf("roles checked=%v", checked)
	}
}

func TestRunPaginationAPIRejectsInvalidAndKeepsHistoricalDetail(t *testing.T) {
	f := newAPIFixture(t)
	users, _ := f.database.ListUsers(context.Background())
	var cookieUser domain.User
	for _, u := range users {
		if u.Role == domain.RolePlatformAdmin {
			cookieUser = u
		}
	}
	cookie := f.session(cookieUser.ID)
	for _, query := range []string{"page=0", "page=-1", "page=1.5", "page=", "pageSize=0", "pageSize=101", "pageSize=no", "filter=unknown"} {
		response := f.request("GET", "/api/v1/runs?"+query, nil, cookie)
		if response.Code != 400 {
			t.Fatalf("%s=%d", query, response.Code)
		}
	}
	response := f.request("GET", "/api/v1/runs", nil, cookie)
	var page struct {
		Items                 []json.RawMessage `json:"items"`
		Page, PageSize, Total int
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Page != 1 || page.PageSize != 50 {
		t.Fatalf("page %s %v", response.Body.String(), err)
	}
	if strings.Contains(response.Body.String(), "logTail") {
		t.Fatal("full run response")
	}
	for _, u := range users {
		if u.Role != domain.RoleEnvironmentOwner {
			response = f.request("GET", "/api/v1/approvals/batch-candidates", nil, f.session(u.ID))
			if response.Code != 403 {
				t.Fatal(fmt.Sprintf("%s candidates=%d", u.Role, response.Code))
			}
		}
	}
}
