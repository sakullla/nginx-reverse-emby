package relay

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

func copyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(writerWithoutReaderFrom{Writer: dst}, readerWithoutWriterTo{Reader: src})
}

type writerWithoutReaderFrom struct {
	io.Writer
}
