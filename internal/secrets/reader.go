package secrets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/Battleroid/snap-share/internal/blob"
)

// partsReader reads a secret's content chunks, stored as separate blobs, as
// one seekable stream (for http.ServeContent and Range requests). Blobs are
// opened lazily, one at a time.
type partsReader struct {
	ctx   context.Context
	store blob.Store
	parts []part
	start []int64 // offset of each part
	size  int64
	off   int64

	cur    blob.File
	curIdx int
}

func newPartsReader(ctx context.Context, store blob.Store, ps []part) *partsReader {
	pr := &partsReader{ctx: ctx, store: store, parts: ps, start: make([]int64, len(ps)), curIdx: -1}
	for i, p := range ps {
		pr.start[i] = pr.size
		pr.size += p.Size
	}
	return pr
}

func (pr *partsReader) Read(b []byte) (int, error) {
	if pr.off >= pr.size {
		return 0, io.EOF
	}
	if len(b) == 0 {
		return 0, nil
	}
	i := sort.Search(len(pr.start), func(i int) bool { return pr.start[i] > pr.off }) - 1
	if i != pr.curIdx {
		if pr.cur != nil {
			_ = pr.cur.Close()
			pr.cur = nil
		}
		f, err := pr.store.Open(pr.ctx, pr.parts[i].Key)
		if err != nil {
			return 0, fmt.Errorf("secrets: open chunk: %w", err)
		}
		if f.Size() != pr.parts[i].Size {
			_ = f.Close()
			return 0, errors.New("secrets: chunk size does not match the ledger")
		}
		pr.cur, pr.curIdx = f, i
	}
	within := pr.off - pr.start[i]
	b = b[:min(int64(len(b)), pr.parts[i].Size-within)]
	n, err := pr.cur.ReadAt(b, within)
	pr.off += int64(n)
	if n == len(b) {
		return n, nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (pr *partsReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = pr.off + offset
	case io.SeekEnd:
		abs = pr.size + offset
	default:
		return 0, errors.New("secrets: bad whence")
	}
	if abs < 0 {
		return 0, errors.New("secrets: negative position")
	}
	pr.off = abs
	return abs, nil
}

func (pr *partsReader) Close() error {
	if pr.cur == nil {
		return nil
	}
	err := pr.cur.Close()
	pr.cur, pr.curIdx = nil, -1
	return err
}
