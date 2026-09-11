package commands

import "strings"

type resumeCmd struct{}

func (resumeCmd) Name() string        { return "resume" }
func (resumeCmd) Aliases() []string   { return nil }
func (resumeCmd) Description() string { return "Resume an active session by ID or name" }
func (resumeCmd) Metadata() Metadata {
	return Metadata{Usage: "/resume [id|name]", Category: "Session", Policy: PolicyDeferred}
}
func (resumeCmd) Execute(ctx *Context) (*Result, error) {
	target := strings.TrimSpace(ctx.RawArgs)
	if target == "" {
		return ResultOf(OpenResumeAction{}), nil
	}
	return ResultOf(ResumeSessionAction{Target: target}), nil
}

type renameCmd struct{}

func (renameCmd) Name() string        { return "rename" }
func (renameCmd) Aliases() []string   { return nil }
func (renameCmd) Description() string { return "Rename the current session" }
func (renameCmd) Metadata() Metadata {
	return Metadata{Usage: "/rename <name>", Category: "Session", Policy: PolicyDeferred}
}
func (renameCmd) Execute(ctx *Context) (*Result, error) {
	name := strings.TrimSpace(ctx.RawArgs)
	if name == "" {
		return MessageResult("usage: /rename <name>"), nil
	}
	return ResultOf(RenameSessionAction{Name: name}), nil
}

type archiveCmd struct{}

func (archiveCmd) Name() string        { return "archive" }
func (archiveCmd) Aliases() []string   { return nil }
func (archiveCmd) Description() string { return "Archive a session (current by default)" }
func (archiveCmd) Metadata() Metadata {
	return Metadata{Usage: "/archive [id|name]", Category: "Session", Policy: PolicyDeferred}
}
func (archiveCmd) Execute(ctx *Context) (*Result, error) {
	return ResultOf(ArchiveSessionAction{Target: strings.TrimSpace(ctx.RawArgs)}), nil
}

type deleteCmd struct{}

func (deleteCmd) Name() string        { return "delete" }
func (deleteCmd) Aliases() []string   { return nil }
func (deleteCmd) Description() string { return "Permanently delete a session (current by default)" }
func (deleteCmd) Metadata() Metadata {
	return Metadata{Usage: "/delete [id|name]", Category: "Session", Policy: PolicyDeferred}
}
func (deleteCmd) Execute(ctx *Context) (*Result, error) {
	return ResultOf(DeleteSessionAction{Target: strings.TrimSpace(ctx.RawArgs)}), nil
}

func RegisterSessionCommands(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(resumeCmd{})
	reg.Register(renameCmd{})
	reg.Register(archiveCmd{})
	reg.Register(deleteCmd{})
}
