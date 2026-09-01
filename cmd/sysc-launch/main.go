package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	launcher "github.com/Nomadcxx/sysc-launch"
)

const (
	usage        = "usage: sysc-launch query [QUERY]\n       sysc-launch launch DESKTOP_ID [ACTION_ID]\n"
	queryTimeout = 5 * time.Second
)

type queryService interface {
	Query(string)
	Results() <-chan []launcher.Result
	Activate(string, string) error
	Close()
}

type serviceFactory func(logf func(string, ...any)) queryService

func defaultService(logf func(string, ...any)) queryService {
	return launcher.NewService(launcher.ServiceConfig{
		History: launcher.DefaultHistory(logf),
		Logf:    logf,
	})
}

func run(
	args []string,
	stdout, stderr io.Writer,
	newService serviceFactory,
	timeout time.Duration,
) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "query":
		if len(args) > 2 {
			fmt.Fprintln(stderr, "sysc-launch: query accepts at most one argument")
			fmt.Fprint(stderr, usage)
			return 2
		}
	case "launch":
		if len(args) < 2 || args[1] == "" {
			fmt.Fprintln(stderr, "sysc-launch: launch requires a desktop ID")
			fmt.Fprint(stderr, usage)
			return 2
		}
		if len(args) > 3 {
			fmt.Fprintln(stderr, "sysc-launch: launch accepts at most a desktop ID and action ID")
			fmt.Fprint(stderr, usage)
			return 2
		}
	default:
		fmt.Fprintf(stderr, "sysc-launch: unknown command %q\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}

	logf := func(format string, values ...any) {
		fmt.Fprintf(stderr, format+"\n", values...)
	}
	service := newService(logf)
	asyncClose := false
	defer func() {
		if asyncClose {
			// ponytail: Scan and Rank callbacks do not accept context, and Activate
			// has no caller-provided context, so async cleanup keeps this short-lived
			// CLI bounded; add context-aware service methods for forced cancellation.
			go service.Close()
			return
		}
		service.Close()
	}()

	results, err := waitForResults(service.Results(), timeout)
	if err != nil {
		asyncClose = errors.Is(err, context.DeadlineExceeded)
		fmt.Fprintf(stderr, "sysc-launch: initial results: %v\n", err)
		return 1
	}

	if args[0] == "launch" {
		action := ""
		if len(args) == 3 {
			action = args[2]
		}
		if err := activate(service, args[1], action, timeout); err != nil {
			asyncClose = errors.Is(err, context.DeadlineExceeded)
			fmt.Fprintf(stderr, "sysc-launch: activation: %v\n", err)
			return 1
		}
		return 0
	}

	if len(args) == 2 && args[1] != "" {
		service.Query(args[1])
		results, err = waitForResults(service.Results(), timeout)
		if err != nil {
			asyncClose = errors.Is(err, context.DeadlineExceeded)
			fmt.Fprintf(stderr, "sysc-launch: query results: %v\n", err)
			return 1
		}
	}

	if results == nil {
		results = []launcher.Result{}
	}
	if err := json.NewEncoder(stdout).Encode(results); err != nil {
		fmt.Fprintf(stderr, "sysc-launch: encode results: %v\n", err)
		return 1
	}
	return 0
}

func waitForResults(results <-chan []launcher.Result, timeout time.Duration) ([]launcher.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case result, ok := <-results:
		if !ok {
			return nil, fmt.Errorf("service closed")
		}
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func activate(service queryService, id, action string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- service.Activate(id, action)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultService, queryTimeout))
}
