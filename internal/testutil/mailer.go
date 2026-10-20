package testutil

import (
	"context"
	"sync"

	"attendance-api/internal/notify"
)

type MemoryMailer struct {
	mu       sync.Mutex
	messages []notify.Message
}

func (m *MemoryMailer) Send(ctx context.Context, message notify.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, message)
	return nil
}

func (m *MemoryMailer) Sent() []notify.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	messages := make([]notify.Message, len(m.messages))
	copy(messages, m.messages)
	return messages
}
