package oci

import (
	"context"
	"fmt"
	"io"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
)

const maxBlobReadRetries = 5

// resumableTarget recovers failures after response headers have arrived, which
// the registry's request-level retry transport cannot handle. ORAS still checks
// the descriptor size and digest before promoting the artifact out of staging.
type resumableTarget struct {
	oras.ReadOnlyTarget
	progress TransferProgress
}

func (t resumableTarget) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	body, err := t.ReadOnlyTarget.Fetch(ctx, desc)
	if err != nil || desc.Size <= 0 {
		return body, err
	}
	return &resumableBlob{
		ctx: ctx, body: body, size: desc.Size, progress: t.progress,
		fetch: func() (io.ReadCloser, error) { return t.ReadOnlyTarget.Fetch(ctx, desc) },
	}, nil
}

type resumableBlob struct {
	ctx      context.Context
	body     io.ReadCloser
	fetch    func() (io.ReadCloser, error)
	size     int64
	offset   int64
	retries  int
	readErr  error
	progress TransferProgress
}

func (r *resumableBlob) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.readErr != nil {
			if r.retries >= maxBlobReadRetries {
				return 0, fmt.Errorf("blob download interrupted at byte %d after %d retries: %w", r.offset, r.retries, r.readErr)
			}
			r.retries++
			reportTransferStatus(r.progress, "Retrying interrupted OCI blob at byte %d (attempt %d/%d)", r.offset, r.retries, maxBlobReadRetries)
			// Best-effort cleanup of the failed response; a close error must not
			// prevent retrying the interrupted read.
			_ = r.body.Close()
			timer := time.NewTimer(time.Second << (r.retries - 1))
			select {
			case <-r.ctx.Done():
				timer.Stop()
				return 0, r.ctx.Err()
			case <-timer.C:
			}
			body, err := r.fetch()
			if err != nil {
				return 0, err
			}
			r.body = body
			if r.offset > 0 {
				if seeker, ok := body.(io.Seeker); ok {
					_, err = seeker.Seek(r.offset, io.SeekStart)
				} else {
					// Registries without byte ranges can still recover, at the cost
					// of reading the prefix again. It never counts as new progress.
					_, err = io.CopyN(io.Discard, body, r.offset)
				}
				if err != nil {
					r.readErr = err
					continue
				}
			}
			r.readErr = nil
		}
		n, err := r.body.Read(p)
		r.offset += int64(n)
		if err == nil || (err == io.EOF && r.offset >= r.size) {
			return n, err
		}
		r.readErr = err
		if n > 0 {
			return n, nil
		}
	}
}

func (r *resumableBlob) Close() error { return r.body.Close() }
