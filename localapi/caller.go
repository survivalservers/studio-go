package localapi

import "context"

type callerKey struct{}

// WithCaller marks a request's context with the game server whose process made the call, as the host found it when
// the connection was made (the Windows agent: the process at the connection's far end and the game server whose job
// holds it). With Options.CheckCaller, a token is taken only from its own game server's processes.
func WithCaller(ctx context.Context, gameServer string) context.Context {
	return context.WithValue(ctx, callerKey{}, gameServer)
}

// CallerOf is the game server WithCaller marked a request's context with.
func CallerOf(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(callerKey{}).(string)
	return id, ok && id != ""
}
