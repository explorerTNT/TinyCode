package permissions

import (
	"bufio"
	"fmt"
	"os"
	"sync"
)

// defaultInput is the stdin-backed fallback used when no InputFunc is wired
// in (plain console mode). It is guarded so a concurrent stdin read does not
// race with another caller.
var (
	stdinMu   sync.Mutex
	stdinRdr  *bufio.Reader
	stdinOnce sync.Once
)

func defaultInput(prompt string) (string, error) {
	if prompt != "" {
		fmt.Print(prompt)
	}
	stdinOnce.Do(func() { stdinRdr = bufio.NewReader(os.Stdin) })
	stdinMu.Lock()
	defer stdinMu.Unlock()
	line, err := stdinRdr.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}
