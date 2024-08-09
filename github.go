package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
	yaml "gopkg.in/yaml.v2"
	"sigs.k8s.io/kustomize/api/types"
)

// GitHub, or more typically GitHub Instance as we call in our codebase,
// is a GitHub + GitOps client that can be used to interact with GitHub
// to do GitOps.
//
// It has methods to get files, create pull requests, request reviews,
// get kustomization, and so on.
//
// This is used by deploy models so that each deploy model
// does not need to implement the same logic to interact with GitHub.
type GitHub struct {
	client        githubv4.Client
	httpClient    *http.Client
	org           string
	repo          string
	defaultBranch string
}

type GitHubInput struct {
	Repository string
	Branch     string
}

func CreateGitHubInstance(url, token, org, repo, defaultBranch string) GitHub {
	src := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	httpClient := oauth2.NewClient(context.Background(), src)

	var client *githubv4.Client
	if url != "" {
		client = githubv4.NewEnterpriseClient(url, httpClient)
	} else {
		client = githubv4.NewClient(httpClient)
	}
	return GitHub{*client, httpClient, org, repo, defaultBranch}
}

func (g GitHub) GetFile(path string) (b []byte, err error) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", g.org, g.repo, path), nil)
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	b, err = io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	return
}

func (g GitHub) GetUsers() (users map[string]string, err error) {
	users = map[string]string{}
	type user struct {
		ID    string
		Login string
	}
	var query struct {
		Organization struct {
			MembersWithRole struct {
				Nodes []user
			} `graphql:"membersWithRole(first: 100)"`
		} `graphql:"organization(login: $org)"`
	}
	variables := map[string]interface{}{
		"org": githubv4.String(g.org),
	}

	err = g.client.Query(context.Background(), &query, variables)
	if err != nil {
		return map[string]string{}, err
	}
	for _, node := range query.Organization.MembersWithRole.Nodes {
		users[node.Login] = node.ID
	}
	return
}

func (g GitHub) GetKustomization(path string) (obj types.Kustomization, err error) {
	b, err := g.GetFile(path)
	if err != nil {
		return
	}
	err = yaml.Unmarshal(b, &obj)
	if err != nil {
		err = fmt.Errorf("[ERROR] The file should be kustomization format")
		return
	}
	return
}

func (g GitHub) ListBranch(name string) ([]string, error) {
	type refs struct {
		Name string
	}
	var query struct {
		Repository struct {
			Refs struct {
				Nodes []refs
			} `graphql:"refs(first: 50, refPrefix: \"refs/heads/\")"`
		} `graphql:"repository(owner: $org, name: $name)"`
	}
	variables := map[string]interface{}{
		"name": githubv4.String(name),
		"org":  githubv4.String(g.org),
	}

	err := g.client.Query(context.Background(), &query, variables)
	if err != nil {
		return []string{}, err
	}
	var arr []string
	for _, v := range query.Repository.Refs.Nodes {
		arr = append(arr, v.Name)
	}
	return arr, nil
}

func (g GitHub) GitHash(branch string) (string, error) {
	var query struct {
		Repository struct {
			Ref struct {
				Target struct {
					CommitUrl string
				}
			} `graphql:"ref(qualifiedName: $branch)"`
		} `graphql:"repository(owner: $org, name: $repo)"`
	}
	variables := map[string]interface{}{
		"repo":   githubv4.String(g.repo),
		"branch": githubv4.String(branch),
		"org":    githubv4.String(g.org),
	}

	err := g.client.Query(context.Background(), &query, variables)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(query.Repository.Ref.Target.CommitUrl, fmt.Sprintf("https://github.com/%s/%s/commit/", g.org, g.repo)), nil
}

func (g GitHub) RepositoryID() (string, error) {
	var query struct {
		Repository struct {
			ID string
		} `graphql:"repository(owner: $org, name: $repo)"`
	}
	variables := map[string]interface{}{
		"repo": githubv4.String(g.repo),
		"org":  githubv4.String(g.org),
	}

	err := g.client.Query(context.Background(), &query, variables)
	if err != nil {
		return "", err
	}
	return query.Repository.ID, nil
}

func (g GitHub) BranchID(refName string) (string, error) {
	var query struct {
		Repository struct {
			Ref struct {
				ID string
			} `graphql:"ref(qualifiedName: $ref)"`
		} `graphql:"repository(owner: $org, name: $repo)"`
	}
	variables := map[string]interface{}{
		"repo": githubv4.String(g.repo),
		"org":  githubv4.String(g.org),
		"ref":  githubv4.String(refName),
	}

	err := g.client.Query(context.Background(), &query, variables)
	if err != nil {
		return "", err
	}
	return query.Repository.Ref.ID, nil
}

func (g GitHub) CreatePullRequest(branch string, title string, description string) (string, int, error) {
	repoID, err := g.RepositoryID()
	if err != nil {
		return "", -1, err
	}
	var mutate struct {
		CreatePullRequest struct {
			PullRequest struct {
				ID          string
				BaseRefName string
				HeadRefName string
				Title       string
				Body        string
				Number      int
			}
		} `graphql:"createPullRequest(input:$input)"`
	}
	body := githubv4.String(fmt.Sprintf("from bot\n\n%s", description))
	modify := githubv4.Boolean(true)
	refName := "refs/heads/master"
	if g.defaultBranch != "" {
		refName = g.defaultBranch
	}
	input := githubv4.CreatePullRequestInput{
		RepositoryID:        repoID,
		BaseRefName:         githubv4.String(refName),
		HeadRefName:         githubv4.String(branch), //"refs/heads/bot/test-update"
		Title:               githubv4.String(title),  //"Deploy super staging",
		Body:                &body,
		MaintainerCanModify: &modify,
	}
	err = g.client.Mutate(context.Background(), &mutate, input, nil)
	return mutate.CreatePullRequest.PullRequest.ID, mutate.CreatePullRequest.PullRequest.Number, err
}

func (g GitHub) UpdatePullRequest(prID string, assigneeIDs string) error {
	var mutate struct {
		UpdatePullRequest struct {
			PullRequest struct {
				ID string
			}
		} `graphql:"updatePullRequest(input:$input)"`
	}
	ids := []githubv4.ID{assigneeIDs}
	input := githubv4.UpdatePullRequestInput{
		PullRequestID: prID,
		AssigneeIDs:   &ids,
	}
	return g.client.Mutate(context.Background(), &mutate, input, nil)
}

func (g GitHub) RequestReviews(prID string, assigneeIDs string) error {
	var mutate struct {
		RequestReviews struct {
			PullRequest struct {
				ID string
			}
		} `graphql:"requestReviews(input:$input)"`
	}
	ids := []githubv4.ID{assigneeIDs}
	input := githubv4.RequestReviewsInput{
		PullRequestID: prID,
		UserIDs:       &ids,
	}
	return g.client.Mutate(context.Background(), &mutate, input, nil)
}

func (g GitHub) MergePullRequest(prID string) error {
	var mutate struct {
		MergePullRequest struct {
			PullRequest struct {
				ID string
			}
		} `graphql:"mergePullRequest(input:$input)"`
	}
	input := githubv4.MergePullRequestInput{
		PullRequestID: prID,
	}
	return g.client.Mutate(context.Background(), &mutate, input, nil)
}

func (g GitHub) ClosePullRequest(prID string) error {
	var mutate struct {
		ClosePullRequest struct {
			PullRequest struct {
				ID string
			}
		} `graphql:"closePullRequest(input:$input)"`
	}
	input := githubv4.ClosePullRequestInput{
		PullRequestID: prID,
	}
	if err := g.client.Mutate(context.Background(), &mutate, input, nil); err != nil {
		// Without this, we end up seeing an unhelpful error message from gocat like the below when we fail to close a pull request:
		//
		// 	[INFO] Action Value: deploy_kustomize_reject|PR_hogehoge_2_bot/docker-image-tag-project-foo-staging-14e308b
		// 	Could not resolve to a node with the global id of 'PR'
		// 	[ERROR] Internal Server Error
		//
		// "Internal Server Error" is emitted in handler.go and "Could not resolve..." is emitted by GitHub API.
		// The message lacks context and is not helpful for debugging.
		// We want to see that the error is caused by the failure to close a pull request.
		// We also want to see the inputs to the mutation to see which side of the mutation failed, us or GitHub.
		return fmt.Errorf("unable to close pull request: input=%v, err=%v", input, err)
	}
	return nil
}

func (g GitHub) DeleteBranch(refName string) error {
	refID, err := g.BranchID(refName)
	if err != nil {
		return err
	}
	var mutate struct {
		DeleteRef struct {
			ClientMutationID string
		} `graphql:"deleteRef(input: $input)"`
	}
	input := githubv4.DeleteRefInput{
		RefID: refID,
	}
	return g.client.Mutate(context.Background(), &mutate, input, nil)
}

type Commit struct {
	ID            string
	Oid           string
	Message       string
	CommittedDate time.Time
}

func (g GitHub) Commits(repo string, branch string) ([]Commit, error) {
	type edge struct {
		Node Commit
	}
	var query struct {
		Repository struct {
			Ref struct {
				Target struct {
					Commit struct {
						History struct {
							Edges []edge
						} `graphql:"history(first: 100)"`
					} `graphql:"... on Commit"`
				}
			} `graphql:"ref(qualifiedName: $branch)"`
		} `graphql:"repository(owner: $org, name: $repo)"`
	}
	variables := map[string]interface{}{
		"repo":   githubv4.String(repo),
		"branch": githubv4.String(branch),
		"org":    githubv4.String(g.org),
	}

	err := g.client.Query(context.Background(), &query, variables)
	if err != nil {
		return []Commit{}, err
	}

	commits := []Commit{}
	for _, e := range query.Repository.Ref.Target.Commit.History.Edges {
		commits = append(commits, e.Node)
	}
	return commits, nil
}

type GitHubCommitsBetweenInput struct {
	Repository    string
	Branch        string
	FirstCommitID string
	LastCommitID  string
}

func (g GitHub) CommitsBetween(input GitHubCommitsBetweenInput) ([]Commit, error) {
	output := []Commit{}

	commits, err := g.Commits(input.Repository, input.Branch)
	if err != nil {
		return output, err
	}

	between := false
	for _, c := range commits {
		if c.Oid == input.LastCommitID {
			between = true
		}
		if c.Oid == input.FirstCommitID {
			break
		}
		if between {
			output = append(output, c)
		}
	}

	return output, nil
}

type GitHubGetPullRequestInput struct {
	GitHubInput
	Number int
}

type PullRequest struct {
	ID       string
	Number   int
	Body     string
	BodyHTML string `graphql:"bodyHTML"`
}

func (g GitHub) GetPullRequest(input GitHubGetPullRequestInput) (PullRequest, error) {
	repo := input.Repository
	if repo == "" {
		repo = g.repo
	}
	var query struct {
		Repository struct {
			PullRequest PullRequest `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $org, name: $repo)"`
	}
	variables := map[string]interface{}{
		"repo":   githubv4.String(repo),
		"org":    githubv4.String(g.org),
		"number": githubv4.Int(input.Number),
	}

	err := g.client.Query(context.Background(), &query, variables)
	if err != nil {
		return PullRequest{}, err
	}

	return query.Repository.PullRequest, nil
}
