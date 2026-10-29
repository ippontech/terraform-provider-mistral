// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// TestFindWorkspaceTellsArchivedFromGone checks the status findWorkspace
// gives for an active, an archived and a missing workspace. Read removes the
// last two alike and warns only for the archived one, a warning the resource
// tests cannot see: only this status tells them apart.
func TestFindWorkspaceTellsArchivedFromGone(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	// The default list holds the active workspace; is_archived=true adds the
	// archived one, as the fake of fake_test.go does.
	srv.Handle("GET /v1/admin/workspaces", func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{{"uuid": "ws-active", "name": "active"}}
		if r.URL.Query().Get("is_archived") == "true" {
			items = append(items, map[string]any{"uuid": "ws-archived", "name": "archived"})
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
	})
	cl := client.New(client.Config{BaseURL: srv.URL, AdminAPIKey: sdktest.TestAdminKey})

	cases := map[string]struct {
		id   string
		want workspaceStatus
	}{
		"active":   {"ws-active", workspaceActive},
		"archived": {"ws-archived", workspaceArchived},
		"gone":     {"ws-gone", workspaceGone},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ws, status, err := findWorkspace(context.Background(), cl, c.id, name)
			if err != nil || status != c.want {
				t.Fatalf("findWorkspace(%s) = status %d, %v; want status %d", c.id, status, err, c.want)
			}
			if (ws != nil) != (c.want == workspaceActive) {
				t.Errorf("findWorkspace(%s) workspace = %v, want one only when active", c.id, ws)
			}
		})
	}
}
