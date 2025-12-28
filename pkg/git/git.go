package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func InGitDir(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s is not a git repository", path)
	}

	return nil
}

func GetHEADSha(ctx context.Context, path string) (string, error) {
	hashCmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "HEAD")

	hashBytes, err := hashCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get initial HEAD: %w", err)
	}

	initialHash := strings.TrimSpace(string(hashBytes))

	return initialHash, nil
}
