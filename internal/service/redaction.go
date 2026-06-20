package service

import "regexp"

var (
	emailPattern = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	urlPattern   = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+\b`)
	phonePattern = regexp.MustCompile(`(?i)(?:\+?\d[\d\s().-]{8,}\d)`)
	tokenPattern = regexp.MustCompile(`\b[A-Za-z0-9_-]{24,}\b`)
)

func RedactText(text string) string {
	text = emailPattern.ReplaceAllString(text, "[email]")
	text = urlPattern.ReplaceAllString(text, "[url]")
	text = phonePattern.ReplaceAllString(text, "[phone]")
	text = tokenPattern.ReplaceAllString(text, "[token]")
	return text
}
