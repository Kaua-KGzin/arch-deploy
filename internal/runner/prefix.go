package runner

import (
	"bytes"
	"io"
	"sync"
)

// PrefixWriter prefixes every line with a label, so the output of hosts that
// deploy in parallel stays readable. Writers that share mu never interleave
// within a line.
type PrefixWriter struct {
	mu     *sync.Mutex
	w      io.Writer
	prefix []byte
	buf    []byte
}

func NewPrefixWriter(w io.Writer, mu *sync.Mutex, prefix string) *PrefixWriter {
	return &PrefixWriter{mu: mu, w: w, prefix: []byte(prefix)}
}

func (p *PrefixWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		p.emit(p.buf[:i+1])
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}

// Flush writes a trailing partial line, if any.
func (p *PrefixWriter) Flush() {
	if len(p.buf) > 0 {
		p.emit(append(p.buf, '\n'))
		p.buf = nil
	}
}

func (p *PrefixWriter) emit(line []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.w.Write(p.prefix)
	p.w.Write(line)
}
