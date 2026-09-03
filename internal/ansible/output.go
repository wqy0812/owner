package ansible

import (
	"fmt"
	"io"
	"os"
	"time"
)

// A private file separates process exit from log persistence and inherited pipe
// handles. Followers still stream output while the process runs, then drain the
// completed files before recap parsing and Run completion. Raw output has the
// same private, temporary lifetime as the workspace's credential-bearing inputs.
type phaseOutput struct {
	writer *os.File
	reader *os.File
	sink   io.Writer
}

func newPhaseOutput(dir string, sink io.Writer) (*phaseOutput, error) {
	writer, err := os.CreateTemp(dir, "phase-output-")
	if err != nil {
		return nil, fmt.Errorf("create private output: %w", err)
	}
	reader, err := os.Open(writer.Name())
	if err != nil {
		writer.Close()
		os.Remove(writer.Name())
		return nil, fmt.Errorf("open private output: %w", err)
	}
	return &phaseOutput{writer: writer, reader: reader, sink: sink}, nil
}

func (o *phaseOutput) close() {
	o.reader.Close()
	o.writer.Close()
	os.Remove(o.writer.Name())
}

func (o *phaseOutput) follow(processDone <-chan struct{}) <-chan error {
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := io.Copy(o.sink, o.reader); err != nil {
				done <- fmt.Errorf("read process output: %w", err)
				return
			}
			select {
			case <-processDone:
				// Output may have arrived between the previous EOF and exit.
				_, err := io.Copy(o.sink, o.reader)
				if err != nil {
					err = fmt.Errorf("drain process output: %w", err)
				}
				done <- err
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}
