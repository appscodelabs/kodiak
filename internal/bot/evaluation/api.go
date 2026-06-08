// PRAPI is the per-PR side-effect interface that mergeable uses to
// dequeue/requeue, set status, update branches, label, comment, and merge.
//
// Ported from the Protocol class PRAPI in bot/kodiak/evaluation.py.

package evaluation

import "context"

// PRAPI is the interface mergeable() drives. The bot-side adapter lives in
// pullrequest.go and wraps queries.Client plus the queue callbacks.
type PRAPI interface {
	Dequeue(ctx context.Context) error
	Requeue(ctx context.Context) error
	SetStatus(ctx context.Context, msg string, latestCommitSHA string, markdownContent string) error
	PullRequestsForRef(ctx context.Context, ref string) (int, bool, error)
	DeleteBranch(ctx context.Context, branch string) error
	RemoveLabel(ctx context.Context, label string) error
	AddLabel(ctx context.Context, label string) error
	CreateComment(ctx context.Context, body string) error
	TriggerTestCommit(ctx context.Context) error
	Merge(ctx context.Context, mergeMethod, commitTitle, commitMessage string) error
	QueueForMerge(ctx context.Context) (int, bool, error)
	UpdateBranch(ctx context.Context) error
	ApprovePullRequest(ctx context.Context) error
}
