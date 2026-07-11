package topic

import (
	"fmt"
	"strings"
)

const MinWords = 4

// Validate rejects topics that are too short to produce a useful article.
func Validate(value string) error {
	wordCount := len(strings.Fields(strings.TrimSpace(value)))
	if wordCount < MinWords {
		return fmt.Errorf("topic must contain at least %d words (got %d)", MinWords, wordCount)
	}
	return nil
}
