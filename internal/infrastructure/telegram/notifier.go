package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"admin-svc/internal/domain"
)

type Notifier struct {
	botToken string
	chatID   string
	client   *http.Client
}

type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

type CommandHandler func(ctx context.Context, input string) (string, error)

type getUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID int              `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	Text string       `json:"text"`
	Chat telegramChat `json:"chat"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

func New(botToken, chatID string) *Notifier {
	botToken = normalizeBotToken(botToken)
	chatID = strings.TrimSpace(chatID)

	return &Notifier{
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *Notifier) Send(notification domain.Notification) error {
	return n.sendWithModeToChat(notification.ChatID, notification.Message, "")
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

func (n *Notifier) SendPlain(text string) error {
	return n.sendWithMode(text, "")
}

func (n *Notifier) StartCommandListener(ctx context.Context, handlers map[string]CommandHandler) {
	if n.botToken == "" || n.chatID == "" {
		log.Printf("[telegram] command listener disabled: token/chat_id missing")
		return
	}

	log.Printf("[telegram] command listener started")
	offset := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("[telegram] command listener stopped")
			return
		default:
		}

		updates, err := n.getUpdates(ctx, offset, 25)
		if err != nil {
			log.Printf("[telegram] getUpdates error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		for _, upd := range updates {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}
			n.handleUpdate(ctx, upd, handlers)
		}
	}
}

func (n *Notifier) send(text string) error {
	return n.sendWithMode(text, "Markdown")
}

func (n *Notifier) sendWithMode(text, parseMode string) error {
	return n.sendWithModeToChat("", text, parseMode)
}

func (n *Notifier) sendWithModeToChat(chatID, text, parseMode string) error {
	if n.botToken == "" {
		return fmt.Errorf("telegram bot token is empty")
	}

	targetChatID := strings.TrimSpace(chatID)
	if targetChatID == "" {
		targetChatID = n.chatID
	}
	if targetChatID == "" {
		return fmt.Errorf("telegram chat id is empty")
	}

	payload := sendMessageRequest{
		ChatID:    targetChatID,
		Text:      text,
		ParseMode: parseMode,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.botToken)
	resp, err := n.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if len(b) > 0 {
			return fmt.Errorf("telegram responded with status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		}
		return fmt.Errorf("telegram responded with status %d", resp.StatusCode)
	}

	log.Printf("[telegram] message sent: %q", truncate(text, 60))
	return nil
}

func (n *Notifier) handleUpdate(ctx context.Context, upd telegramUpdate, handlers map[string]CommandHandler) {
	if upd.Message == nil {
		return
	}

	if strconv.FormatInt(upd.Message.Chat.ID, 10) != n.chatID {
		return
	}

	text := strings.TrimSpace(upd.Message.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}

	commandText := strings.Fields(text)[0]
	input := strings.TrimSpace(text[len(commandText):])

	command := commandText
	if i := strings.Index(command, "@"); i > 0 {
		command = command[:i]
	}

	handler, ok := handlers[command]
	if !ok {
		_ = n.SendPlain("Unknown command. Available: /status /restart /blog_gen /tik_users")
		return
	}

	result, err := handler(ctx, input)
	if err != nil {
		_ = n.SendPlain(fmt.Sprintf("%s failed: %v", command, err))
		return
	}
	if strings.TrimSpace(result) == "" {
		result = command + " completed"
	}
	_ = n.SendPlain(result)
}

func (n *Notifier) getUpdates(ctx context.Context, offset, timeoutSeconds int) ([]telegramUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=%d", n.botToken, offset, timeoutSeconds)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call getUpdates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var payload getUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode getUpdates: %w", err)
	}
	if !payload.OK {
		return nil, fmt.Errorf("getUpdates returned ok=false")
	}

	return payload.Result, nil
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
