package telegram

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type Notifier struct {
	botToken string
	chatID   string
	client   *http.Client
	maxRetries int
	retryDelay time.Duration
}

type telegramAPIError struct {
	StatusCode int
	Body       string
}

func (e *telegramAPIError) Error() string {
	if strings.TrimSpace(e.Body) != "" {
		return fmt.Sprintf("telegram responded with status %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
	}
	return fmt.Sprintf("telegram responded with status %d", e.StatusCode)
}

type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

func New(botToken, chatID string) *Notifier {
	botToken = normalizeBotToken(botToken)
	chatID = strings.TrimSpace(chatID)

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &Notifier{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout:   15 * time.Second,
			Transport: transport,
		},
		maxRetries: 2,
		retryDelay: 700 * time.Millisecond,
	}
}

func (n *Notifier) SendAlert(checkType, name, detail string) error {
	emoji := "🔴"
	text := fmt.Sprintf(
		"%s *[ALERT] %s*\n\n*Check:* `%s`\n*Detail:* %s\n*Time:* `%s`",
		emoji,
		checkType,
		name,
		escapeMarkdown(detail),
		time.Now().Format("2006-01-02 15:04:05"),
	)
	return n.send(text)
}

func (n *Notifier) SendRecovery(checkType, name string) error {
	text := fmt.Sprintf(
		"✅ *[RECOVERED] %s*\n\n*Check:* `%s`\n*Time:* `%s`",
		checkType,
		name,
		time.Now().Format("2006-01-02 15:04:05"),
	)
	return n.send(text)
}

func (n *Notifier) SendStartup(checks []string) error {
	list := ""
	for _, c := range checks {
		list += fmt.Sprintf("  • %s\n", c)
	}
	text := fmt.Sprintf(
		"🚀 *Admin Service Started*\n\nMonitoring:\n%s\n*Time:* `%s`",
		list,
		time.Now().Format("2006-01-02 15:04:05"),
	)
	return n.send(text)
}

func (n *Notifier) send(text string) error {
	if n.botToken == "" {
		return fmt.Errorf("telegram bot token is empty")
	}
	if n.chatID == "" {
		return fmt.Errorf("telegram chat id is empty")
	}

	payload := sendMessageRequest{
		ChatID:    n.chatID,
		Text:      text,
		ParseMode: "Markdown",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= n.maxRetries; attempt++ {
		if attempt > 0 {
			wait := n.retryDelay * time.Duration(attempt)
			log.Printf("[telegram] retrying send (attempt %d/%d) in %s", attempt+1, n.maxRetries+1, wait)
			time.Sleep(wait)
		}

		if err := n.sendOnce(body); err != nil {
			lastErr = err
			if !isRetryable(err) {
				return err
			}
			continue
		}

		log.Printf("[telegram] message sent: %q", truncate(text, 60))
		return nil
	}

	return lastErr
}

func (n *Notifier) sendOnce(body []byte) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.botToken)
	resp, err := n.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &telegramAPIError{
			StatusCode: resp.StatusCode,
			Body:       string(b),
		}
	}

	return nil
}

func isRetryable(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var apiErr *telegramAPIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		if apiErr.StatusCode >= http.StatusInternalServerError {
			return true
		}
	}

	return false
}

func escapeMarkdown(s string) string {
	// Escape special Markdown chars for Telegram
	replacer := []struct{ old, new string }{
		{"_", "\\_"}, {"*", "\\*"}, {"`", "\\`"}, {"[", "\\["},
	}
	for _, r := range replacer {
		s = replaceAll(s, r.old, r.new)
	}
	return s
}

func replaceAll(s, old, new string) string {
	var buf bytes.Buffer
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			buf.WriteString(new)
			i += len(old)
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func normalizeBotToken(token string) string {
	token = strings.TrimSpace(token)
	// Some users provide "bot<token>" from cURL examples.
	return strings.TrimPrefix(token, "bot")
}
