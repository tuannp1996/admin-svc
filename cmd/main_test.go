package main

import (
	"strings"
	"testing"

	"admin-svc/internal/client"
)

func TestFormatBlogArticleIncludesCoverAndPreview(t *testing.T) {
	article := client.BlogArticle{
		ID:     42,
		Status: "published",
		Title:  "Example article",
		FrontMatter: map[string]interface{}{
			"coverImage": "https://cdn.example.com/cover.jpg",
			"previewUrl": "https://example.com/articles/example.md",
		},
	}

	got := formatBlogArticle(article)
	for _, want := range []string{
		"coverImage: https://cdn.example.com/cover.jpg",
		"Preview: https://example.com/articles/example.md",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatBlogArticle() missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatBlogArticlePrefersTopLevelURLs(t *testing.T) {
	article := client.BlogArticle{
		ID:          7,
		CoverImage:  "https://cdn.example.com/top-level.jpg",
		MarkdownURL: "https://example.com/top-level.md",
		FrontMatter: map[string]interface{}{
			"coverImage": "https://cdn.example.com/front-matter.jpg",
			"previewUrl": "https://example.com/front-matter.md",
		},
	}

	got := formatBlogArticle(article)
	if !strings.Contains(got, article.CoverImage) || !strings.Contains(got, article.MarkdownURL) {
		t.Fatalf("formatBlogArticle() did not prefer top-level fields:\n%s", got)
	}
}
