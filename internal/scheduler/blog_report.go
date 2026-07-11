package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"admin-svc/internal/client"
	"admin-svc/internal/config"
	"admin-svc/internal/usecase/port"

	"github.com/robfig/cron/v3"
)

type BlogReport struct {
	config   config.BlogReportConfig
	notifier port.Notifier
	admin    *client.BlogAdminClient
}

func NewBlogReport(cfg config.BlogReportConfig, notifier port.Notifier, admin *client.BlogAdminClient) *BlogReport {
	return &BlogReport{config: cfg, notifier: notifier, admin: admin}
}

func (r *BlogReport) Start() func() {
	if r == nil || !r.config.Enabled {
		return func() {}
	}

	spec := strings.TrimSpace(r.config.Cron)
	if spec == "" {
		spec = "0 30 8 * * *"
	}
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	c := cron.New(cron.WithParser(parser), cron.WithLocation(time.Local))
	if _, err := c.AddFunc(spec, r.run); err != nil {
		log.Printf("[blog-report] invalid cron spec=%q: %v", spec, err)
		return func() {}
	}
	c.Start()
	log.Printf("[blog-report] scheduled spec=%q timezone=%s", spec, time.Local)
	return func() { _ = c.Stop() }
}

func (r *BlogReport) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	message, err := BuildBlogReport(ctx, r.admin, time.Now())
	if err != nil {
		log.Printf("[blog-report] build failed: %v", err)
		return
	}
	if err := r.notifier.SendPlain(message); err != nil {
		log.Printf("[blog-report] telegram send failed: %v", err)
	}
}

func BuildBlogReport(ctx context.Context, admin *client.BlogAdminClient, now time.Time) (string, error) {
	pending, pendingTotal, err := admin.ListWithTotal(ctx, "pending", 1000)
	if err != nil {
		return "", fmt.Errorf("list pending articles: %w", err)
	}
	published, publishedTotal, err := admin.ListWithTotal(ctx, "published", 1000)
	if err != nil {
		return "", fmt.Errorf("list published articles: %w", err)
	}

	cutoff := now.Add(-24 * time.Hour)
	newPublished := make([]client.BlogArticle, 0)
	for _, article := range published {
		publishedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(article.PublishedAt))
		if err == nil && !publishedAt.Before(cutoff) && !publishedAt.After(now) {
			newPublished = append(newPublished, article)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("BLOG REPORT - %s\n\n", now.Format("02/01/2006 15:04")))
	b.WriteString(fmt.Sprintf("Summary\n- Articles displaying: %d\n- Pending approval: %d\n- Newly displayed in 24h: %d", publishedTotal, pendingTotal, len(newPublished)))
	b.WriteString("\n\nPending approval")
	writeBlogReportArticles(&b, pending)
	b.WriteString("\n\nNewly displayed in the last 24h")
	writeBlogReportArticles(&b, newPublished)
	return b.String(), nil
}

func writeBlogReportArticles(b *strings.Builder, articles []client.BlogArticle) {
	if len(articles) == 0 {
		b.WriteString("\n- None")
		return
	}
	for _, article := range articles {
		b.WriteString(fmt.Sprintf("\n- #%d %s", article.ID, article.Title))
		if article.Slug != "" {
			b.WriteString(" (" + article.Slug + ")")
		}
	}
}
