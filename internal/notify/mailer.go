package notify

import "context"

type Message struct {
	To      string
	Subject string
	Text    string
}

type Mailer interface {
	Send(context.Context, Message) error
}
