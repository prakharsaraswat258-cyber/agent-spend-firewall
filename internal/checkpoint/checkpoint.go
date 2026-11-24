// Package checkpoint saves a repo's working state to a WIP branch without
// touching the user's branch, index or working tree.
package checkpoint

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func git(dir string, env []string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), env...)
	out, err := c.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, s)
	}
	return s, nil
}

func Branch(run string) string { return "pitstop/wip/" + run }

// Make commits the current working tree (tracked + untracked, minus .gitignored)
// onto refs/heads/pitstop/wip/<run>. Returns branch and commit. If nothing
// changed since the last checkpoint it returns that one.
func Make(dir, run, msg string) (branch, commit string, err error) {
	branch = Branch(run)
	head, err := git(dir, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("repo has no commits yet: %w", err)
	}
	tmp, err := os.CreateTemp("", "pitstop-index-*")
	if err != nil {
		return "", "", err
	}
	tmp.Close()
	os.Remove(tmp.Name()) // git wants to create it itself
	defer os.Remove(tmp.Name())
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	if _, err = git(dir, env, "read-tree", head); err != nil {
		return "", "", err
	}
	if _, err = git(dir, env, "add", "-A"); err != nil {
		return "", "", err
	}
	tree, err := git(dir, env, "write-tree")
	if err != nil {
		return "", "", err
	}
	if prev, e := git(dir, nil, "rev-parse", "--verify", "refs/heads/"+branch); e == nil {
		if pt, e := git(dir, nil, "rev-parse", prev+"^{tree}"); e == nil && pt == tree {
			return branch, prev, nil
		}
		head = prev // chain checkpoints
	}
	commit, err = git(dir, []string{"GIT_AUTHOR_NAME=pitstop", "GIT_AUTHOR_EMAIL=pitstop@localhost",
		"GIT_COMMITTER_NAME=pitstop", "GIT_COMMITTER_EMAIL=pitstop@localhost"},
		"commit-tree", tree, "-p", head, "-m", msg)
	if err != nil {
		return "", "", err
	}
	if _, err = git(dir, nil, "update-ref", "refs/heads/"+branch, commit); err != nil {
		return "", "", err
	}
	return branch, commit, nil
}
