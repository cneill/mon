package git

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
)

var ErrNotGitRepo = errors.New("not a git repository")

func OpenGitRepo(path string) (*git.Repository, error) {
	repo, err := git.PlainOpen(path)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return nil, ErrNotGitRepo
	} else if err != nil {
		return nil, fmt.Errorf("failed to open git repo: %w", err)
	}

	return repo, nil
}

func GetHEADSHA(repo *git.Repository) (string, error) {
	headRef, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD commit: %w", err)
	}

	return headRef.Hash().String(), nil
}
