package testutil

import (
	"context"
	"testing"

	"attendance-api/internal/notify"
)

func TestMemoryMailerRecordsAndCopiesMessages(t *testing.T) {
	t.Parallel()
	mailer := &MemoryMailer{}
	message := notify.Message{To: "student@example.test", Subject: "Verify", Text: "code"}
	if err := mailer.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	got := mailer.Sent()
	if len(got) != 1 || got[0] != message {
		t.Fatalf("Sent() = %#v", got)
	}
	got[0].Text = "changed"
	if mailer.Sent()[0].Text != "code" {
		t.Fatal("Sent() returned a mutable view of recorded messages")
	}
}
