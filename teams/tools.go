package teams

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/tools"
)

// NewTeamTools returns the team_load, team_list, team_comment, team_list_comments tools.
// The tools operate against the given registry and loader; loader is used to
// refresh the registry on load/list.
func NewTeamTools(registry *Registry, teamsPath string) []*tools.Tool {
	return []*tools.Tool{
		teamLoadTool(registry, teamsPath),
		teamListTool(registry, teamsPath),
		teamCommentTool(registry, teamsPath),
		teamListCommentsTool(registry, teamsPath),
	}
}

func teamLoadTool(registry *Registry, teamsPath string) *tools.Tool {
	return tools.NewTool("team_load",
		tools.WithDescription("Load a team by name and mark it as the active team. Use team_list to discover available teams."),
		tools.WithString("name", tools.Required(), tools.Description("Team name")),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			name, _ := req.Arguments["name"].(string)
			if name == "" {
				return tools.NewToolResultError("missing required parameter: name"), nil
			}
			if err := registry.loader.Load(); err != nil {
				return tools.NewToolResultError(err.Error()), nil
			}
			registry.Refresh()
			if !registry.SetActive(name) {
				return tools.NewToolResultError(fmt.Sprintf("team not found: %s", name)), nil
			}
			t, _ := registry.Get(name)
			return tools.NewToolResultText(formatTeamLoaded(t)), nil
		}),
	)
}

func teamListTool(registry *Registry, teamsPath string) *tools.Tool {
	return tools.NewTool("team_list",
		tools.WithDescription("List all available teams with their members."),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			if err := registry.loader.Load(); err != nil {
				return tools.NewToolResultError(err.Error()), nil
			}
			registry.Refresh()
			teams := registry.List()
			if len(teams) == 0 {
				return tools.NewToolResultText("No teams available. Define one under " + teamsPath + "/{name}/team.json."), nil
			}
			var b strings.Builder
			active := registry.ActiveName()
			for _, t := range teams {
				marker := " "
				if t.Name == active {
					marker = "*"
				}
				fmt.Fprintf(&b, "%s %s — %s\n", marker, t.Name, t.Description)
				for _, m := range t.Members {
					fmt.Fprintf(&b, "    - %s (%s)\n", m.Name, m.Role)
				}
			}
			return tools.NewToolResultText(b.String()), nil
		}),
	)
}

func teamCommentTool(registry *Registry, teamsPath string) *tools.Tool {
	return tools.NewTool("team_comment",
		tools.WithDescription("Post a comment to the active team's collective log. Use this to surface questions, progress notes, or reviews to teammates."),
		tools.WithString("text", tools.Required(), tools.Description("Comment body")),
		tools.WithString("to", tools.Description("Comma-separated recipient names (optional)")),
		tools.WithString("kind", tools.Description("Comment kind: review|note|question|progress")),
		tools.WithString("proposal_id", tools.Description("Anchor: proposal ID (optional)")),
		tools.WithString("task_id", tools.Description("Anchor: task ID (optional)")),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			t := registry.Active()
			if t == nil {
				return tools.NewToolResultError("no active team; call team_load first"), nil
			}
			text, _ := req.Arguments["text"].(string)
			if text == "" {
				return tools.NewToolResultError("missing required parameter: text"), nil
			}
			kind, _ := req.Arguments["kind"].(string)
			toStr, _ := req.Arguments["to"].(string)
			var to []string
			for _, s := range strings.Split(toStr, ",") {
				if s = strings.TrimSpace(s); s != "" {
					to = append(to, s)
				}
			}
			propID, _ := req.Arguments["proposal_id"].(string)
			taskID, _ := req.Arguments["task_id"].(string)
			c := Comment{
				From: "agent",
				To:   to,
				Text: text,
				Kind: CommentKind(kind),
				Anchor: Anchor{
					ProposalID: propID,
					TaskID:     taskID,
				},
			}
			if err := AppendComment(teamsPath, t.Name, c); err != nil {
				return tools.NewToolResultError(err.Error()), nil
			}
			return tools.NewToolResultText(fmt.Sprintf("comment posted to team %s", t.Name)), nil
		}),
	)
}

func teamListCommentsTool(registry *Registry, teamsPath string) *tools.Tool {
	return tools.NewTool("team_list_comments",
		tools.WithDescription("Read comments from the active team's collective log, optionally filtered by proposal/task/author/kind."),
		tools.WithString("proposal_id", tools.Description("Filter by anchor proposal ID")),
		tools.WithString("task_id", tools.Description("Filter by anchor task ID")),
		tools.WithString("from", tools.Description("Filter by author")),
		tools.WithString("kind", tools.Description("Filter by kind")),
		tools.WithNumber("limit", tools.Description("Max number of comments to return")),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			t := registry.Active()
			if t == nil {
				return tools.NewToolResultError("no active team; call team_load first"), nil
			}
			f := CommentFilter{}
			if v, ok := req.Arguments["proposal_id"].(string); ok {
				f.ProposalID = v
			}
			if v, ok := req.Arguments["task_id"].(string); ok {
				f.TaskID = v
			}
			if v, ok := req.Arguments["from"].(string); ok {
				f.From = v
			}
			if v, ok := req.Arguments["kind"].(string); ok {
				f.Kind = CommentKind(v)
			}
			if v, ok := req.Arguments["limit"].(float64); ok {
				f.Limit = int(v)
			}
			comments, err := QueryComments(teamsPath, t.Name, f)
			if err != nil {
				return tools.NewToolResultError(err.Error()), nil
			}
			if len(comments) == 0 {
				return tools.NewToolResultText("no comments match"), nil
			}
			var b strings.Builder
			for _, c := range comments {
				anchor := ""
				if c.Anchor.TaskID != "" {
					anchor = fmt.Sprintf(" [%s/%s]", c.Anchor.ProposalID, c.Anchor.TaskID)
				} else if c.Anchor.ProposalID != "" {
					anchor = fmt.Sprintf(" [%s]", c.Anchor.ProposalID)
				}
				to := strings.Join(c.To, ",")
				if to != "" {
					to = " → " + to
				}
				fmt.Fprintf(&b, "%s %s%s%s: %s\n", c.TS.Format("2006-01-02 15:04"), c.From, to, anchor, c.Text)
			}
			return tools.NewToolResultText(b.String()), nil
		}),
	)
}

func formatTeamLoaded(t *Team) string {
	if t == nil {
		return "team loaded"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Loaded team: %s\n%s\n\nMembers:\n", t.Name, t.Description)
	for _, m := range t.Members {
		fmt.Fprintf(&b, "  - %s (%s)\n", m.Name, m.Role)
	}
	return b.String()
}
