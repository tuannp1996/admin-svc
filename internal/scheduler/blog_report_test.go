package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"admin-svc/internal/client"
)

func TestBuildBlogReport(t *testing.T) {
	now := time.Date(2026, 7, 12, 8, 30, 0, 0, time.FixedZone("UTC+7", 7*60*60))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("status") {
		case "pending":
			fmt.Fprint(w, `{"articles":[{"id":1,"title":"Needs review","slug":"needs-review"}],"total":1}`)
		case "published":
			fmt.Fprintf(w, `{"articles":[{"id":2,"title":"New article","slug":"new-article","published_at":%q},{"id":3,"title":"Old article","published_at":%q}],"total":12}`,
				now.Add(-time.Hour).Format(time.RFC3339), now.Add(-48*time.Hour).Format(time.RFC3339))
		default:
			http.Error(w, "unexpected status", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	report, err := BuildBlogReport(context.Background(), client.NewBlogAdmin(server.URL, 5), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Articles displaying: 12", "Pending approval: 1", "Newly displayed in 24h: 1", "Needs review", "New article"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "Old article") {
		t.Fatalf("report includes article published before cutoff:\n%s", report)
	}
}
