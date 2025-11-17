package signal

import "context"

func HandleTerminalContext() context.Context {
	ch := HandleTerminalSignal()
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-ch
		cancel()
	}()

	return ctx
}
