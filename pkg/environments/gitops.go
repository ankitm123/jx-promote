package environments

import (
	"context"
	"io/ioutil"
	"os"
	"sort"

	"github.com/jenkins-x/go-scm/scm"
	"github.com/jenkins-x/jx-helpers/v3/pkg/gitclient"
	"github.com/jenkins-x/jx-helpers/v3/pkg/scmhelpers"
	"github.com/jenkins-x/jx-helpers/v3/pkg/stringhelpers"
	"github.com/jenkins-x/jx-helpers/v3/pkg/termcolor"
	"github.com/pkg/errors"

	"github.com/jenkins-x/jx-logging/v3/pkg/log"
)

const (
	// LabelUpdatebot is the label applied to PRs created by updatebot
	LabelUpdatebot = "updatebot"
)

// Create a pull request against the environment repository for env.
// The EnvironmentPullRequestOptions are used to provide a Gitter client for performing git operations,
// a GitProvider client for talking to the git provider,
// a callback ModifyChartFn which is where the changes you want to make are defined.
// The branchNameText defines the branch name used, the title is used for both the commit and the pull request title,
// the message as the body for both the commit and the pull request,
// and the pullRequestInfo for any existing PR that exists to modify the environment that we want to merge these
// changes into.
func (o *EnvironmentPullRequestOptions) Create(gitURL, prDir string, pullRequestDetails *scm.PullRequest, autoMerge bool) (*scm.PullRequest, error) {
	scmClient, repoFullName, err := o.GetScmClient(gitURL, o.GitKind)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create ScmClient")
	}
	if scmClient == nil {
		return nil, nil
	}

	existingPr, err := o.FindExistingPullRequest(scmClient, repoFullName)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find existing PullRequest")
	}

	if prDir == "" {
		tempDir, err := ioutil.TempDir("", "create-pr")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tempDir)
	}

	cloneGitURL := gitURL
	if o.Fork {
		cloneGitURL, err = o.EnsureForked(scmClient, repoFullName)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to ensure repository is forked %s", gitURL)
		}
	}
	cloneGitURLSafe := cloneGitURL
	if o.ScmClientFactory.GitToken != "" && o.ScmClientFactory.GitUsername != "" {
		cloneGitURL, err = o.ScmClientFactory.CreateAuthenticatedURL(cloneGitURL)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create authenticated git URL to clone with for private repositories")
		}
	}

	dir, err := gitclient.CloneToDir(o.Gitter, cloneGitURL, "")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to clone git URL %s", cloneGitURLSafe)
	}

	if o.Fork {
		err = o.rebaseForkFromUpstream(dir, gitURL)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to rebase forked repository")
		}
	}

	o.OutDir = dir
	log.Logger().Debugf("cloned %s to %s", termcolor.ColorInfo(cloneGitURLSafe), termcolor.ColorInfo(dir))

	if existingPr != nil {
		log.Logger().Infof("rebasing existing Pull Request %s", termcolor.ColorInfo(existingPr.Link))

		err = o.checkoutExistingPullRequest(dir, existingPr)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to checkout existing PullRequest")
		}
	}

	currentSha, err := gitclient.GetLatestCommitSha(o.Gitter, dir)
	if err != nil {
		return nil, errors.Wrap(err, "could not get current commit sha")
	}

	if o.Function == nil {
		return nil, errors.Errorf("no change function configured")
	}
	err = o.Function()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to invoke change function in dir %s", dir)
	}

	o.Labels = nil
	// lets merge any labels together...
	if autoMerge {
		o.Labels = append(o.Labels, LabelUpdatebot)
	}
	for _, l := range pullRequestDetails.Labels {
		if l != nil {
			label := l.Name
			if label != "" && stringhelpers.StringArrayIndex(o.Labels, label) < 0 {
				o.Labels = append(o.Labels, label)
			}
		}
	}

	latestSha, err := gitclient.GetLatestCommitSha(o.Gitter, dir)
	if err != nil {
		return nil, errors.Wrap(err, "could not get current latest commit sha")
	}

	doneCommit := true
	if latestSha == currentSha {
		changed, err := gitclient.HasChanges(o.Gitter, dir)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to detect changes in dir %s", dir)
		}
		if !changed {
			// lets avoid failing to create the PR as we really have made changes
			doneCommit = false
		}
	}

	prInfo, err := o.CreatePullRequest(scmClient, gitURL, repoFullName, dir, doneCommit, existingPr)
	if err != nil {
		return prInfo, errors.Wrapf(err, "failed to create pull request in dir %s", dir)
	}
	return prInfo, nil
}

func (o *EnvironmentPullRequestOptions) checkoutExistingPullRequest(dir string, pr *scm.PullRequest) error {
	if o.RemoteName == "" {
		o.RemoteName = "origin"
	}
	if pr.Source == "" {
		log.Logger().Warnf("PullRequest %s does not have a source so we cannot use it", pr.Link)
		return nil
	}

	// set the base branch to the PR branch
	o.BaseBranchName = pr.Source
	o.BranchName = o.BaseBranchName

	// checkout the remote tracking branch
	_, err := o.Git().Command(dir, "checkout", "--track", o.RemoteName+"/"+o.BaseBranchName)
	if err != nil {
		return errors.Wrapf(err, "failed to checkout existing PR branch")
	}

	log.Logger().Infof("checked out branch %s from PullRequest %s", o.BaseBranchName, pr.Link)
	return nil
}

func (o *EnvironmentPullRequestOptions) FindExistingPullRequest(scmClient *scm.Client, repoFullName string) (*scm.PullRequest, error) {
	if o.PullRequestFilter == nil {
		return nil, nil
	}
	ctx := context.Background()
	labels := o.PullRequestFilter.Labels
	prs, _, err := scmClient.PullRequests.List(ctx, repoFullName, &scm.PullRequestListOptions{
		Size:   100,
		Open:   true,
		Labels: labels,
	})
	if scmhelpers.IsScmNotFound(err) || len(prs) == 0 {
		return nil, nil
	}

	// sort in descending order of PR numbers (assumes PRs numbers increment!)
	sort.Slice(prs, func(i, j int) bool {
		pi := prs[i]
		pj := prs[j]
		return pi.Number > pj.Number
	})

	// lets find the latest PR which is not closed
	for i := range prs {
		pr := prs[i]
		if pr.Closed || pr.Merged || pr.Source != repoFullName {
			continue
		}
		found := false
		for _, label := range pr.Labels {
			if stringhelpers.StringArrayIndex(labels, label.Name) >= 0 {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		return pr, nil
	}
	return nil, nil
}
