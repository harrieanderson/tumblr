package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const postsFile = "posts/queue.txt"

func readQueue() ([]string, error) {
	file, err := os.Open(postsFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func writeQueue(lines []string) error {
	out := strings.Join(lines, "\n")
	if len(lines) > 0 {
		out += "\n"
	}
	return os.WriteFile(postsFile, []byte(out), 0644)
}

// peekPost returns the next non-empty line without removing it.
func peekPost() (string, error) {
	lines, err := readQueue()
	if err != nil {
		return "", err
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("no posts left in %s", postsFile)
}

// popPost removes the first non-empty line from the queue.
func popPost() error {
	lines, err := readQueue()
	if err != nil {
		return err
	}

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return writeQueue(lines[i+1:])
	}
	return fmt.Errorf("no posts left in %s", postsFile)
}

func queueHasPosts() bool {
	_, err := peekPost()
	return err == nil
}
