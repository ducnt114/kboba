package k8s

import (
	"bufio"
	"context"

	corev1 "k8s.io/api/core/v1"
)

const (
	// logTailLines is how much history is fetched when a stream starts.
	logTailLines = 500
	// maxLogLineBytes bounds a single log line; longer lines end the stream
	// with bufio.ErrTooLong.
	maxLogLineBytes = 1024 * 1024
)

// StreamLogs follows the logs of one container (GET pods/log?follow=true).
//
// Lines are delivered on the first channel, which is closed when the stream
// ends: the context was cancelled, the container exited or the connection
// dropped. If it ended because of an error (other than cancellation), that
// error is sent on the second channel before the lines channel is closed.
//
// Cancel ctx to stop streaming; this closes the HTTP connection and ends
// the reader goroutine.
func (c *client) StreamLogs(ctx context.Context, namespace, pod, container string) (<-chan string, <-chan error, error) {
	if c.connErr != nil {
		return nil, nil, c.connErr
	}

	tail := int64(logTailLines)
	opts := &corev1.PodLogOptions{Container: container, Follow: true, TailLines: &tail}
	body, err := c.clientset.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return nil, nil, err
	}

	lines := make(chan string, 256)
	errs := make(chan error, 1)
	go func() {
		defer close(lines)
		defer body.Close()

		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 64*1024), maxLogLineBytes)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			errs <- err
		}
	}()
	return lines, errs, nil
}
