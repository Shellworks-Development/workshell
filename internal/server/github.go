package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var githubClient = &http.Client{Timeout: 15 * time.Second}

// githubAPI is overridable in tests.
var githubAPI = "https://api.github.com"

func (s *Server) githubRequest(ctx context.Context, p *project, method, path string, body any, out any) error {
	if p.GitHubRepo == "" || p.githubToken == "" {
		return badRequest("connect a GitHub repository and token in project settings first")
	}
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, githubAPI+"/repos/"+p.GitHubRepo+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.githubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Workshell")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := githubClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		json.Unmarshal(data, &e)
		return fmt.Errorf("GitHub returned HTTP %d: %s", resp.StatusCode, e.Message)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (s *Server) handleCreateGitHubIssue(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	if issue.GitHubURL != "" {
		return errStatus(http.StatusConflict, "this issue is already linked to %s", issue.GitHubURL)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**\n\n", issue.Culprit)
	fmt.Fprintf(&b, "| | |\n|---|---|\n| Events | %d |\n| Players affected | %d |\n| Side | %s |\n| First seen in | `%s` |\n| Last seen in | `%s` |\n\n",
		issue.EventCount, issue.UserCount, issue.Side, issue.FirstRelease, issue.LastRelease)
	latest, _ := s.queryErrorEvents(r, p, `WHERE issue_id = ? ORDER BY ts DESC, id DESC LIMIT 1`, issue.ID)
	if len(latest) > 0 {
		ev := latest[0]
		fmt.Fprintf(&b, "### Message\n```\n%s\n```\n", strings.ReplaceAll(ev.Message, "```", "'''"))
		if len(ev.Frames) > 0 {
			b.WriteString("### Stack trace\n")
			for _, f := range ev.Frames {
				loc := fmt.Sprintf("`%s:%d`", f.Script, f.Line)
				if f.URL != "" {
					loc = fmt.Sprintf("[`%s:%d`](%s)", f.File, f.Line, f.URL)
				}
				if f.Function != "" {
					fmt.Fprintf(&b, "- %s in `%s`\n", loc, f.Function)
				} else {
					fmt.Fprintf(&b, "- %s\n", loc)
				}
			}
		}
	}
	if l := s.link(fmt.Sprintf("/p/%d/issues/%d", p.ID, issue.ID)); l != "" {
		fmt.Fprintf(&b, "\n[View in Workshell](%s)\n", l)
	}
	var created struct {
		Number  int64  `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	err = s.githubRequest(r.Context(), p, http.MethodPost, "/issues", map[string]any{
		"title": truncate(issue.Title, 250),
		"body":  b.String(),
	}, &created)
	if err != nil {
		return badRequest("%v", err)
	}
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE issues SET github_number = ?, github_url = ? WHERE id = ?`, created.Number, created.HTMLURL, issue.ID); err != nil {
		return err
	}
	if err := s.addActivity(r, issue.ID, u, "github", "", map[string]any{"number": created.Number, "url": created.HTMLURL}); err != nil {
		return err
	}
	return ok(w, map[string]any{"number": created.Number, "url": created.HTMLURL})
}

func (s *Server) githubComment(ctx context.Context, p *project, number int64, body string) error {
	return s.githubRequest(ctx, p, http.MethodPost, fmt.Sprintf("/issues/%d/comments", number), map[string]any{"body": body}, nil)
}

// handleGetGitHubIssue fetches the linked GitHub issue's state and comments,
// so the Workshell thread shows the GitHub conversation too.
func (s *Server) handleGetGitHubIssue(w http.ResponseWriter, r *http.Request, p *project) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	if issue.GitHubNumber == nil {
		return errNotFound
	}
	var gh struct {
		State   string `json:"state"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
	}
	if err := s.githubRequest(r.Context(), p, http.MethodGet, fmt.Sprintf("/issues/%d", *issue.GitHubNumber), nil, &gh); err != nil {
		return badRequest("%v", err)
	}
	var comments []struct {
		Body      string `json:"body"`
		HTMLURL   string `json:"html_url"`
		CreatedAt string `json:"created_at"`
		User      struct {
			Login     string `json:"login"`
			AvatarURL string `json:"avatar_url"`
		} `json:"user"`
	}
	if err := s.githubRequest(r.Context(), p, http.MethodGet, fmt.Sprintf("/issues/%d/comments?per_page=100", *issue.GitHubNumber), nil, &comments); err != nil {
		return badRequest("%v", err)
	}
	out := []map[string]any{}
	for _, c := range comments {
		ts, _ := time.Parse(time.RFC3339, c.CreatedAt)
		out = append(out, map[string]any{"author": c.User.Login, "avatar": c.User.AvatarURL, "body": truncate(c.Body, 10_000), "url": c.HTMLURL, "createdAt": ts.UnixMilli()})
	}
	return ok(w, map[string]any{"state": gh.State, "title": gh.Title, "url": gh.HTMLURL, "comments": out})
}
