package internal // change to your package name

import (
	"context"
	"io"
)

const tgChunk = 256 * 1024 // multiple of 4 KB, never crosses a 1 MB boundary when aligned

// streamRange writes bytes [start, end] (inclusive) to w.
func streamRange(
	ctx context.Context,
	w io.Writer,
	start, end int64,
	fetch func(ctx context.Context, offset int64, limit int) ([]byte, error),
) error {
	off := start - start%tgChunk // align down
	skip := start - off
	remaining := end - start + 1

	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := fetch(ctx, off, tgChunk)
		if err != nil {
			return err // don't retry 400s
		}
		if len(data) == 0 {
			return io.ErrUnexpectedEOF
		}
		if skip > 0 {
			if skip >= int64(len(data)) {
				return io.ErrUnexpectedEOF
			}
			data = data[skip:]
			skip = 0
		}
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		remaining -= int64(n)
		off += tgChunk
	}
	return nil
}