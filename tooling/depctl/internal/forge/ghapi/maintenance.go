package ghapi

import (
	"context"
	"errors"
	"github.com/google/go-github/v69/github"
	"os"
)

// NewMaintenance separates repository identity reads/writes from CI reads.
// GH_TOKEN is deliberately mandatory; CI_TOKEN never authorizes comments.
func NewMaintenance(repository string) (*Reader, error) {
	reader, err := NewWithToken(repository, os.Getenv("GH_TOKEN"))
	if err != nil {
		return nil, err
	}
	ci, err := NewWithToken(repository, os.Getenv("CI_TOKEN"))
	if err != nil {
		return nil, err
	}
	reader.ciClient = ci.client
	reader.strictMaintenance = true
	return reader, nil
}

// PostMaintenanceComment writes one already-authorized fixed-command comment
// and validates the receipt. An uncertain response must never be retried blindly.
func (r *Reader) PostMaintenanceComment(ctx context.Context, number int, body string) (int, error) {
	if number <= 0 {
		return 0, errors.New("invalid pull request number")
	}
	comment, _, err := r.client.Issues.CreateComment(ctx, r.owner, r.repo, number, &github.IssueComment{Body: &body})
	if err != nil {
		return 0, err
	}
	if comment == nil || comment.GetID() <= 0 || comment.GetBody() != body {
		return 0, errors.New("hold cancellation response is incomplete; inspect the PR before retrying")
	}
	return int(comment.GetID()), nil
}
