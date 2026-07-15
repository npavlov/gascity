package mailbox

import (
	"fmt"
	"strings"
)

// ThreadContinuationProblem explains why a non-empty upstream thread cursor
// cannot be followed by the current Supervisor API.
const ThreadContinuationProblem = "thread continuation is unavailable because the Supervisor thread API exposes no cursor parameter"

// ProjectCount copies an authoritative count without fabricating defaults for
// partial provider failures.
func ProjectCount(total, unread int64, partial bool, partialErrors []string) Count {
	return Count{
		Total:         total,
		Unread:        unread,
		Partial:       partial,
		PartialErrors: copyStrings(partialErrors),
	}
}

// ProjectPage validates and copies one upstream page while preserving order,
// duplicates, the opaque cursor, and the authoritative total.
func ProjectPage(items []Message, total int64, nextCursor string, partial bool, partialErrors []string) (Page, error) {
	projected, err := projectMessages(items)
	if err != nil {
		return Page{}, err
	}
	return Page{
		Items:         projected,
		Total:         total,
		NextCursor:    nextCursor,
		Partial:       partial,
		PartialErrors: copyStrings(partialErrors),
	}, nil
}

// ProjectMessage validates and copies one message.
func ProjectMessage(message Message) (Message, error) {
	if strings.TrimSpace(message.ID) == "" {
		return Message{}, fmt.Errorf("mail message requires a non-empty ID")
	}
	message.CC = copyStrings(message.CC)
	return message, nil
}

// ProjectThread preserves provider order. A returned continuation cursor is
// surfaced as explicit truncation because Supervisor currently has no cursor
// input on this endpoint.
func ProjectThread(items []Message, total int64, nextCursor string, partial bool, partialErrors []string) (Thread, error) {
	projected, err := projectMessages(items)
	if err != nil {
		return Thread{}, err
	}
	errors := copyStrings(partialErrors)
	truncated := strings.TrimSpace(nextCursor) != ""
	if truncated {
		partial = true
		errors = append(errors, ThreadContinuationProblem)
	}
	return Thread{
		Items:         projected,
		Total:         total,
		Partial:       partial,
		Truncated:     truncated,
		PartialErrors: errors,
	}, nil
}

func projectMessages(items []Message) ([]Message, error) {
	result := make([]Message, 0, len(items))
	for index, item := range items {
		projected, err := ProjectMessage(item)
		if err != nil {
			return nil, fmt.Errorf("mail message %d: %w", index, err)
		}
		result = append(result, projected)
	}
	return result, nil
}

func copyStrings(values []string) []string {
	return append([]string{}, values...)
}
