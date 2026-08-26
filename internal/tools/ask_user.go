package tools

import "os"

// AskUser asks the user a question and returns their response, honoring the
// TINY_CODE_ASK_USER / TINYCODE_ASK_USER auto-answer for tests.
func AskUser(question string, ask func(string) string) string {
	if auto := os.Getenv("TINY_CODE_ASK_USER"); auto != "" {
		return "User's response: " + auto
	}
	if auto := os.Getenv("TINYCODE_ASK_USER"); auto != "" {
		return "User's response: " + auto
	}
	return "User's response: " + ask(question)
}
