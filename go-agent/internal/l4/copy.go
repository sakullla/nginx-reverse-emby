package l4

import "io"

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	if rf, ok := dst.(io.ReaderFrom); ok {
		return rf.ReadFrom(readerWithoutWriterTo{Reader: src})
	}
	return io.Copy(dst, src)
}

type readerWithoutWriterTo struct {
	io.Reader
}
