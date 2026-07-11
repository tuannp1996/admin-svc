package client

import "testing"

func TestParseGeneratedArticle(t *testing.T) {
	detail := `{
		"success": true,
		"data": {
			"article": {
				"id": 42,
				"slug": "phi-dich-vu-sms-banking",
				"front_matter": {"description": "Tóm tắt bài viết"}
			}
		}
	}`

	article, err := ParseGeneratedArticle(detail)
	if err != nil {
		t.Fatal(err)
	}
	if article.ID != "42" {
		t.Fatalf("ID = %q, want 42", article.ID)
	}
	if article.Slug != "phi-dich-vu-sms-banking" {
		t.Fatalf("Slug = %q", article.Slug)
	}
	if article.Summary != "Tóm tắt bài viết" {
		t.Fatalf("Summary = %q", article.Summary)
	}
}

func TestParseGeneratedArticleRequiresIdentifier(t *testing.T) {
	if _, err := ParseGeneratedArticle(`{"data":{"summary":"missing identifier"}}`); err == nil {
		t.Fatal("expected error")
	}
}
