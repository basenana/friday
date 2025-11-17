package signal

import (
	"os"
	"os/signal"
	"syscall"
)

type LifecycleHook func()

var (
	terminalSignalCh = make(chan os.Signal)
	sigusr1Hooks     = make([]LifecycleHook, 0)
	sigusr2Hooks     = make([]LifecycleHook, 0)
)

func HandleTerminalSignal() chan struct{} {
	stopCh := make(chan struct{})
	stopping := false
	signal.Notify(terminalSignalCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		for s := range terminalSignalCh {
			switch s {
			case syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
				if stopping {
					os.Exit(2)
				}
				close(stopCh)
				stopping = true
			case syscall.SIGUSR1:
				for _, hook := range sigusr1Hooks {
					hook()
				}
			case syscall.SIGUSR2:
				for _, hook := range sigusr2Hooks {
					hook()
				}
			}
		}
	}()
	return stopCh
}

func RequestShutdown() {
	terminalSignalCh <- syscall.SIGTERM
}
