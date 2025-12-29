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

// CommitsSince returns all commits after (not including) the given hash.
// It walks from HEAD backwards and stops when it reaches the given hash.
func CommitsSince(repo *git.Repository, sinceHash string) ([]*object.Commit, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference: %w", err)
	}

	// slog.Debug("checking from HEAD hash", "hash", head.Hash().String())

	// If HEAD is the same as sinceHash, no new commits
	if head.Hash().String() == sinceHash {
		return nil, nil
	}

	iter, err := repo.Log(&git.LogOptions{
		From:  head.Hash(),
		Order: git.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk git log: %w", err)
	}
	defer iter.Close()

	var results []*object.Commit

	for {
		commit, err := iter.Next()
		if err != nil {
			break // End of iteration or error
		}

		// slog.Debug("checked commit", "hash", commit.Hash.String())

		// Stop when we reach the starting point
		if commit.Hash.String() == sinceHash {
			break
		}

		results = append(results, commit)
	}

	return results, nil
}

func PatchSince(repo *git.Repository, sinceHash string) (*object.Patch, error) {
	sinceCommit, err := repo.CommitObject(plumbing.NewHash(sinceHash))
	if err != nil {
		return nil, fmt.Errorf("failed to get commit for commit hash %q: %w", sinceHash, err)
	}

	headRef, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference: %w", err)
	}

	headCommit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD commit: %w", err)
	}

	patch, err := sinceCommit.Patch(headCommit)
	if err != nil {
		return nil, fmt.Errorf("failed to get patch between %s and %s: %w", sinceHash, headRef.Hash().String(), err)
	}

	return patch, nil
}

func PatchAddsDeletes(patch *object.Patch) (added int64, deleted int64) { //nolint:nonamedreturns
	stats := patch.Stats()

	for _, fileStat := range stats {
		added += int64(fileStat.Addition)
		deleted += int64(fileStat.Deletion)
	}

	return added, deleted
}
