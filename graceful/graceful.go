// Package graceful shutdown for server
package graceful

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ti/common-go/async"
)

var (
	closers []func(ctx context.Context) error
	signals = make(chan os.Signal, 10)

	// mainGoroutineID is captured once during init(), which always runs on the main goroutine.
	mainGoroutineID uint64
)

func init() {
	mainGoroutineID = goroutineID()
}

// goroutineID returns the current goroutine's ID by parsing runtime.Stack output.
func goroutineID() uint64 {
	buf := make([]byte, 64)
	runtime.Stack(buf, false)
	field := bytes.Fields(buf)[1]
	id, err := strconv.ParseUint(string(field), 10, 64)
	if err != nil {
		panic("graceful: failed to parse goroutine ID: " + err.Error())
	}
	return id
}

// Fn is a function with error.
type Fn func(context.Context) error

// AddCloser add closer.
func AddCloser(closer func(ctx context.Context) error) {
	closers = append(closers, closer)
}

// Close the app gracefully.
func Close() {
	signals <- nil
}

// Start the app, if fn is not empty, it will start fn async.
func Start(ctx context.Context, fn ...Fn) {
	asyncPool := async.New(ctx)
	for _, fv := range fn {
		asyncPool.Async(func(ctx context.Context, fv Fn) (struct{}, error) {
			return struct{}{}, fv(ctx)
		}, fv)
	}
	if err := asyncPool.Await(); err != nil {
		warnJSONLog(err.Error())
		signals <- nil
	}
	// check if in main goroutine
	if goroutineID() == mainGoroutineID {
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		if sig := <-signals; sig != nil {
			warnJSONLog(fmt.Sprintf("closed by %s", sig.String()))
		}
		runClosers()
	}
}

// runClosers close all registered closers.
func runClosers() {
	// Close all closers in the order of first-in-last-out.
	l := len(closers)
	for i := l - 1; i > -1; i-- {
		ctx, cc := context.WithTimeout(context.Background(), 6*time.Minute)
		err := closers[i](ctx)
		cc()
		if err != nil {
			var pathErr *os.PathError
			if errors.As(err, &pathErr) {
				if strings.HasPrefix(pathErr.Path, "/dev/std") {
					continue
				}
			}
			warnJSONLog(err.Error())
		}
	}
}

func warnJSONLog(msg string) {
	_, _ = fmt.Fprintf(os.Stdout, `{"level":"warn","time":"%s","msg":"%s"}`+"\n",
		time.Now().UTC().Format(time.RFC3339), msg)
}
