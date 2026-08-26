package agent

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Reporter is the output sink for the agent: log lines and live status updates.
type Reporter interface {
	Printf(format string, args ...any)
	Println(a ...any)
	Status(text string)
}

// Prompter reads a single line of user input.
type Prompter interface {
	Input(prompt string) (string, error)
}

// IO combines output and input for the agent and its tools.
type IO interface {
	Reporter
	Prompter
}

// ConsoleIO is the plain-terminal IO implementation.
type ConsoleIO struct {
	in  *bufio.Reader
	mu  sync.Mutex
	out *os.File
}

// NewConsoleIO builds a console IO on stdout/stdin.
func NewConsoleIO() *ConsoleIO {
	return &ConsoleIO{in: bufio.NewReader(os.Stdin), out: os.Stdout}
}

func (c *ConsoleIO) Printf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.out, format, args...)
}

func (c *ConsoleIO) Println(a ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintln(c.out, a...)
}

// Status emits a carriage-return live status line (overwritten by the next).
func (c *ConsoleIO) Status(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.out, "\r%s", text)
}

// Input reads a line from stdin.
func (c *ConsoleIO) Input(prompt string) (string, error) {
	if prompt != "" {
		c.Printf("%s", prompt)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := c.in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
