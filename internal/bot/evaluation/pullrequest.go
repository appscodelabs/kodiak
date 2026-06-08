// PRAPI adapter that wraps a queries.Client with the queue.EvaluateInput
// callbacks. Ported from bot/kodiak/pull_request.py:PRV2.

package evaluation

import (
	"context"
	"errors"

	"github.com/appscodelabs/kodiak/internal/bot/queries"
	"github.com/appscodelabs/kodiak/internal/bot/queue"
)

type prAdapter struct {
	client  queries.Client
	number  int
	headSHA string
	in      queue.EvaluateInput
}

func newPRAdapter(client queries.Client, info *queries.EventInfo, in queue.EvaluateInput) *prAdapter {
	return &prAdapter{
		client:  client,
		number:  info.PullRequest.Number,
		headSHA: info.PullRequest.LatestSHA,
		in:      in,
	}
}

func (a *prAdapter) Dequeue(ctx context.Context) error {
	if a.in.Dequeue == nil {
		return nil
	}
	return a.in.Dequeue(ctx)
}

func (a *prAdapter) Requeue(ctx context.Context) error {
	if a.in.Requeue == nil {
		return nil
	}
	return a.in.Requeue(ctx)
}

func (a *prAdapter) SetStatus(ctx context.Context, msg, latestCommitSHA, markdown string) error {
	if latestCommitSHA == "" {
		latestCommitSHA = a.headSHA
	}
	return a.client.CreateOrUpdateCheckRun(ctx, queries.CheckRunInput{
		HeadSHA:    latestCommitSHA,
		Status:     "completed",
		Conclusion: "neutral",
		Title:      msg,
		Summary:    markdown,
	})
}

func (a *prAdapter) PullRequestsForRef(ctx context.Context, ref string) (int, bool, error) {
	prs, err := a.client.GetOpenPullRequests(ctx, queries.OpenPullRequestsFilter{Base: ref})
	if err != nil {
		return 0, false, err
	}
	return len(prs), true, nil
}

func (a *prAdapter) DeleteBranch(_ context.Context, _ string) error {
	// queries.Client doesn't expose DeleteBranch yet — Python source has it,
	// but Kodiak's normal flow uses GitHub's "delete branch on merge" repo
	// setting. Treat as no-op until the REST endpoint is added.
	return nil
}

func (a *prAdapter) RemoveLabel(ctx context.Context, label string) error {
	return a.client.DeleteLabel(ctx, a.number, label)
}

func (a *prAdapter) AddLabel(ctx context.Context, label string) error {
	return a.client.AddLabel(ctx, a.number, label)
}

func (a *prAdapter) CreateComment(ctx context.Context, body string) error {
	return a.client.CreateComment(ctx, a.number, body)
}

func (a *prAdapter) TriggerTestCommit(ctx context.Context) error {
	// The Python source triggers a synthetic test commit by GET-ing the PR.
	// queries.Client doesn't expose that endpoint — no-op for now.
	return nil
}

func (a *prAdapter) Merge(ctx context.Context, mergeMethod, commitTitle, commitMessage string) error {
	return a.client.Merge(ctx, a.number, queries.MergeInput{
		CommitTitle:     commitTitle,
		CommitBody:      commitMessage,
		MergeMethod:     mergeMethod,
		ExpectedHeadOID: a.headSHA,
	})
}

func (a *prAdapter) QueueForMerge(ctx context.Context) (int, bool, error) {
	if a.in.QueueForMerge == nil {
		return 0, false, errors.New("queue_for_merge callback not provided")
	}
	pos, err := a.in.QueueForMerge(ctx)
	if err != nil {
		return 0, false, err
	}
	if pos < 0 {
		return 0, false, nil
	}
	return pos, true, nil
}

func (a *prAdapter) UpdateBranch(ctx context.Context) error {
	return a.client.UpdateBranch(ctx, a.number)
}

func (a *prAdapter) ApprovePullRequest(_ context.Context) error {
	// Python source posts a review with event=APPROVE. queries.Client doesn't
	// expose that yet — no-op stub.
	return nil
}
