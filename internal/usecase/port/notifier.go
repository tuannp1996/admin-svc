package port

import "admin-svc/internal/domain"

type Notifier interface {
    Send(notification domain.Notification) error
    SendAlert(checkType, name, detail string) error
    SendRecovery(checkType, name string) error
}