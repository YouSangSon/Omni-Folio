package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"syscall"
	"time"
)

// Require a complete LF-terminated record, including LF in the size budget.
// ScanLines would silently accept a truncated final transport record.
func splitPaperBundle(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		if i+1 > maxPaperBundleBytes {
			return 0, nil, errors.New("paper stream frame exceeds 4 MiB")
		}
		return i + 1, data[:i+1], nil
	}
	if len(data) >= maxPaperBundleBytes || (atEOF && len(data) > 0) {
		return 0, nil, errors.New("paper stream frame is oversized or missing LF")
	}
	return 0, nil, nil
}

// Own a pollable pipe, one reader goroutine and one sequential execution run.
// No stdout writes can block renewal or prevent the producer seeing closure.
func (s *Service) runLocalPaperStream(ctx context.Context, account, selection string, input *os.File) (resultErr error) {
	defer input.Close()
	if err := input.SetReadDeadline(time.Time{}); err != nil {
		return errors.New("paper stream requires a cancellable input pipe")
	}
	readCtx, cancel := context.WithCancel(ctx)
	type frame struct {
		raw []byte
		err error
	}
	frames := make(chan frame) // At most one pending copied frame; no unbounded queue.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(frames)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), maxPaperBundleBytes+1)
		scanner.Split(splitPaperBundle)
		for scanner.Scan() {
			select {
			case frames <- frame{raw: bytes.Clone(scanner.Bytes())}:
			case <-readCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case frames <- frame{err: errors.New("paper stream input read or framing failed")}:
			case <-readCtx.Done():
			}
		}
	}()
	run := &localPaperRun{service: s, account: account, selection: selection}
	defer func() {
		cancel()
		_ = input.Close() // Pollable Close interrupts a partial-frame read.
		<-done
		// Keep invocation cancellation observable across older redacted read
		// errors, without hiding either validation or independent cleanup errors.
		resultErr = errors.Join(resultErr, ctx.Err(), run.close())
	}()
	ticker := time.NewTicker(paperRunnerLoopHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := run.refresh(ctx); err != nil {
				return err
			}
		case item, ok := <-frames:
			if err := ctx.Err(); err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if item.err != nil {
				return item.err
			}
			proposal, bars, research, err := decodePaperInputBundle(item.raw)
			if err != nil {
				return err
			}
			result, err := run.step(ctx, proposal, bars, research)
			if err != nil {
				return err
			}
			if result.Policy.Decision == "HALT_AND_ROLLBACK" {
				return nil
			}
		}
	}
}

func runLocalPaperStreamCommand(ctx context.Context, args []string, source *os.File) error {
	defer source.Close() // Close original stdin as well, even on preflight failure.
	fs := flag.NewFlagSet("paper-execute-stream", flag.ContinueOnError)
	dbPath := fs.String("db", "omni-folio.db", "existing migrated SQLite database")
	account := fs.String("account", "", "isolated paper account alias")
	selection := fs.String("expected-current-event", "", "exact current selection event")
	arm := fs.Bool("arm-paper", false, "arm once for this invocation; halt on exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !*arm || !orderAlias(*account, "account") || !safeOrderID(*selection) {
		return errors.New("paper stream requires account, expected-current-event and -arm-paper")
	}
	info, err := source.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return errors.New("paper stream stdin must be a pipe")
	}
	// Inherited stdin may be blocking and unpollable. The command owns both
	// descriptors; nonblocking status is intentionally shared with original stdin.
	fd, err := syscall.Dup(int(source.Fd()))
	if err != nil {
		return errors.New("paper stream cannot duplicate input")
	}
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return errors.New("paper stream cannot make input cancellable")
	}
	input := os.NewFile(uintptr(fd), "paper-stream-input")
	defer input.Close()
	if err := input.SetReadDeadline(time.Time{}); err != nil {
		return errors.New("paper stream requires a cancellable input pipe")
	}
	db, err := openExistingDB(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := requireSchema(db); err != nil {
		return err
	}
	return newService(db, time.Now, randomID).runLocalPaperStream(ctx, *account, *selection, input)
}
