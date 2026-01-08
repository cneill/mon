package git

import (
	"errors"
	"fmt"
	"log/slog"

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
		return "", fmt.Errorf("failed to get HEAD reference: %w", err)
	}

	return headRef.Hash().String(), nil
}

func ListFiles(repo *git.Repository) ([]string, error) {
	results := []string{}

	headRef, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference: %w", err)
	}

	headCommit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD commit: %w", err)
	}

	tree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get git tree from HEAD commit: %w", err)
	}

	for _, entry := range tree.Entries {
		results = append(results, entry.Name)
	}

	return results, nil
}

// CommitsSince returns all commits after (not including) the given hash.
// It walks from HEAD backwards and stops when it reaches the given hash.
func CommitsSince(repo *git.Repository, sinceHash string) ([]*object.Commit, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference: %w", err)
	}

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
			break
		}

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

// UnstagedChangeCount returns the count of tracked files with unstaged changes.
// It counts files with Modified, Deleted, or Renamed status in the worktree.
// Untracked files are ignored.
func UnstagedChangeCount(repo *git.Repository) (int64, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return 0, fmt.Errorf("failed to get repo worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return 0, fmt.Errorf("failed to get the status of the git worktree: %w", err)
	}

	var count int64

	for file, fileStatus := range status {
		switch fileStatus.Worktree { //nolint:exhaustive
		case git.Modified, git.Deleted, git.Renamed:
			slog.Debug("unstaged change", "file", file, "status", string(fileStatus.Worktree))

			count++
		}
	}

	return count, nil
}
