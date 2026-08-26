package tools

var (
	ReadOnlySingleCommands = []string{
		"ls", "dir", "cat", "type", "pwd", "whoami", "where",
		"get-location", "get-childitem", "get-content", "cd",
	}
	ReadOnlyPairsCommands = [][2]string{
		{"git", "status"}, {"git", "diff"}, {"git", "log"},
		{"git", "branch"}, {"git", "remote"}, {"git", "--version"},
		{"python", "--version"}, {"node", "--version"},
		{"npm", "--version"}, {"pip", "list"}, {"pip", "--version"},
	}
)
