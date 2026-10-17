package actor

import (
	"context"

	"git.hypo.xyz/agents/friday/core/providers"
	"git.hypo.xyz/agents/friday/core/types"
)

// fakeProvider is a no-op providers.Client used only to satisfy
// session.New during tests. The actor never invokes it directly.
type fakeProvider struct{}

func (fakeProvider) Completion(context.Context, providers.Request) providers.Response {
	return &fakeResponse{}
}
func (fakeProvider) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", nil
}
func (fakeProvider) StructuredPredict(context.Context, providers.Request, any) error {
	return nil
}

type fakeResponse struct{}

func (r *fakeResponse) Message() <-chan providers.Delta { return nil }
func (r *fakeResponse) Error() <-chan error             { return nil }
func (r *fakeResponse) Tokens() providers.Tokens        { return providers.Tokens{} }

var _ providers.Client = fakeProvider{}

// unused guard so imports stay
var _ = types.Event{}
