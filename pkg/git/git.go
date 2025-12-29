package git

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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

func CommitsSince(repo *git.Repository, startHash string) ([]*object.Commit, error) {
	results := []*object.Commit{}

	iter, err := repo.Log(&git.LogOptions{
		From:  plumbing.NewHash(startHash),
		Order: git.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk git log: %w", err)
	}

	defer iter.Close()

	for commit, err := iter.Next(); err != nil; commit, err = iter.Next() {
		results = append(results, commit)
	}

	return results, nil
}
