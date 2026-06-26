package notification

import (
    "admin-svc/internal/domain"
    "admin-svc/internal/usecase/port"
)

type Service struct {
    notifier port.Notifier
}

func New(n port.Notifier) *Service {
    return &Service{notifier: n}
}

func (s *Service) Send(msg string, chatID string) error {
    return s.notifier.Send(domain.Notification{
        Message: msg,
        ChatID:  chatID,
    })
}