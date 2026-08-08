package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

var stdinReader = bufio.NewReader(os.Stdin)

// askText prints a label and reads one line of input.
func askText(label string) (string, error) {
	fmt.Print(label)
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// askConfirm asks a yes/no question. Empty input yields def; y/yes is true,
// anything else is false.
func askConfirm(label string, def bool) (bool, error) {
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	line, err := askText(label + suffix)
	if err != nil {
		return false, err
	}
	if line == "" {
		return def, nil
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// askChoice prints a numbered list and returns the selected index. Empty
// input yields def (when def is a valid index); out-of-range or non-numeric
// input re-prompts.
func askChoice(label string, items []string, def int) (int, error) {
	for {
		fmt.Println(label)
		for i, it := range items {
			fmt.Printf("  %d) %s\n", i+1, it)
		}
		prompt := "Select number"
		if def >= 0 && def < len(items) {
			prompt += fmt.Sprintf(" [%d]", def+1)
		}
		prompt += ": "
		line, err := askText(prompt)
		if err != nil {
			return 0, err
		}
		if line == "" && def >= 0 && def < len(items) {
			return def, nil
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(items) {
			fmt.Printf("Please enter a number between 1 and %d.\n", len(items))
			continue
		}
		return n - 1, nil
	}
}

// askPassword reads a hidden password from /dev/tty (works even when stdin
// is piped).
func askPassword(label string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("cannot open /dev/tty for password prompt: %w", err)
	}
	defer tty.Close()
	fmt.Fprint(tty, label)
	pw, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		return "", err
	}
	return string(pw), nil
}
